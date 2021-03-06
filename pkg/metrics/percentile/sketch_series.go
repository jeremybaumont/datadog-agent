// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2017 Datadog, Inc.

//
// NOTE: This module contains a feature in development that is NOT supported.
//

package percentile

import (
	"bytes"
	"encoding/json"
	"expvar"

	"github.com/gogo/protobuf/proto"

	agentpayload "github.com/DataDog/agent-payload/gogen"
	"github.com/DataDog/datadog-agent/pkg/serializer/marshaler"
)

var sketchSeriesExpvar = expvar.NewMap("SketchSeries")

// Sketch represents a quantile sketch at a specific time
type Sketch struct {
	Timestamp int64   `json:"timestamp"`
	Sketch    QSketch `json:"qsketch"`
}

// SketchSeries holds an array of sketches.
type SketchSeries struct {
	Name       string   `json:"metric"`
	Tags       []string `json:"tags"`
	Host       string   `json:"host"`
	Interval   int64    `json:"interval"`
	Sketches   []Sketch `json:"sketches"`
	ContextKey string   `json:"-"`
}

// SketchSeriesList represents a list of SketchSeries ready to be serialize
type SketchSeriesList []*SketchSeries

// QSketch is a wrapper around GKArray to make it easier if we want to try a
// different sketch algorithm
type QSketch struct {
	GKArray
}

// NewQSketch creates a new QSketch
func NewQSketch() QSketch {
	return QSketch{NewGKArray()}
}

// Add a value to the qsketch
func (q QSketch) Add(v float64) QSketch {
	return QSketch{GKArray: q.GKArray.Add(v)}
}

// NoSketchError is the error returned when not enough samples have been
//submitted to generate a sketch
type NoSketchError struct{}

func (e NoSketchError) Error() string {
	return "Not enough samples to generate sketches"
}

// UnmarshalSketchSeries deserializes a protobuf byte array into sketch series
func UnmarshalSketchSeries(payload []byte) ([]*SketchSeries, agentpayload.CommonMetadata, error) {
	sketches := []*SketchSeries{}
	decodedPayload := &agentpayload.SketchPayload{}
	err := proto.Unmarshal(payload, decodedPayload)
	if err != nil {
		return sketches, agentpayload.CommonMetadata{}, err
	}
	for _, s := range decodedPayload.Sketches {
		sketches = append(sketches,
			&SketchSeries{
				Name:     s.Metric,
				Tags:     s.Tags,
				Host:     s.Host,
				Sketches: unmarshalSketches(s.Distributions),
			})
	}
	return sketches, decodedPayload.Metadata, err
}

func unmarshalSketches(payloadSketches []agentpayload.SketchPayload_Sketch_Distribution) []Sketch {
	sketches := []Sketch{}
	for _, s := range payloadSketches {
		sketches = append(sketches,
			Sketch{
				Timestamp: s.Ts,
				Sketch: QSketch{
					GKArray{Min: s.Min,
						Count:    int64(s.Cnt),
						Max:      s.Max,
						Avg:      s.Avg,
						Sum:      s.Sum,
						Entries:  unmarshalEntries(s.V, s.G, s.Delta),
						Incoming: s.Buf}},
			})
	}
	return sketches
}

// UnmarshalJSONSketchSeries deserializes sketch series from JSON
func UnmarshalJSONSketchSeries(b []byte) ([]*SketchSeries, error) {
	data := make(map[string][]*SketchSeries, 0)
	r := bytes.NewReader(b)
	err := json.NewDecoder(r).Decode(&data)
	if err != nil {
		return []*SketchSeries{}, err
	}
	return data["sketch_series"], nil
}

func marshalSketches(sketches []Sketch) []agentpayload.SketchPayload_Sketch_Distribution {
	sketchesPayload := []agentpayload.SketchPayload_Sketch_Distribution{}

	for _, s := range sketches {
		v, g, delta := marshalEntries(s.Sketch.Entries)
		sketchesPayload = append(sketchesPayload,
			agentpayload.SketchPayload_Sketch_Distribution{
				Ts:    s.Timestamp,
				Cnt:   int64(s.Sketch.Count),
				Min:   s.Sketch.Min,
				Max:   s.Sketch.Max,
				Avg:   s.Sketch.Avg,
				Sum:   s.Sketch.Sum,
				V:     v,
				G:     g,
				Delta: delta,
				Buf:   s.Sketch.Incoming,
			})
	}
	return sketchesPayload
}

// Marshal serializes sketch series using protocol buffers
func (sl SketchSeriesList) Marshal() ([]byte, error) {
	payload := &agentpayload.SketchPayload{
		Sketches: []agentpayload.SketchPayload_Sketch{},
		Metadata: agentpayload.CommonMetadata{},
	}
	for _, s := range sl {
		payload.Sketches = append(payload.Sketches,
			agentpayload.SketchPayload_Sketch{
				Metric:        s.Name,
				Host:          s.Host,
				Distributions: marshalSketches(s.Sketches),
				Tags:          s.Tags,
			})
	}
	return proto.Marshal(payload)
}

// MarshalJSON serializes sketch series to JSON so it can be sent to
// v1 endpoints
func (sl SketchSeriesList) MarshalJSON() ([]byte, error) {
	data := map[string][]*SketchSeries{
		"sketch_series": sl,
	}
	reqBody := &bytes.Buffer{}
	err := json.NewEncoder(reqBody).Encode(data)
	return reqBody.Bytes(), err
}

// SplitPayload breaks the payload into times number of pieces
func (sl SketchSeriesList) SplitPayload(times int) ([]marshaler.Marshaler, error) {
	sketchSeriesExpvar.Add("TimesSplit", 1)
	// Only break it down as much as possible
	if len(sl) < times {
		sketchSeriesExpvar.Add("SketchSeriesListShorter", 1)
		times = len(sl)
	}
	splitPayloads := make([]marshaler.Marshaler, times)
	batchSize := len(sl) / times
	n := 0
	for i := 0; i < times; i++ {
		var end int
		// In many cases the batchSize is not perfect
		// so the last one will be a bit bigger or smaller than the others
		if i < times-1 {
			end = n + batchSize
		} else {
			end = len(sl)
		}
		newSL := SketchSeriesList(sl[n:end])
		splitPayloads[i] = newSL
		n += batchSize
	}
	return splitPayloads, nil
}
