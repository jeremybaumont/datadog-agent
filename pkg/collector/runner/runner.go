// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2017 Datadog, Inc.

package runner

import (
	"expvar"
	"fmt"
	"path"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/datadog-agent/pkg/aggregator"
	"github.com/DataDog/datadog-agent/pkg/collector/check"
	"github.com/DataDog/datadog-agent/pkg/metrics"
	"github.com/DataDog/datadog-agent/pkg/util"
	log "github.com/cihub/seelog"
)

const (
	DEFAULT_NUM_WORKERS               = 6
	MAX_NUM_WORKERS                   = 100
	stopCheckTimeout    time.Duration = 500 * time.Millisecond // Time to wait for a check to stop
)

// checkStats holds the stats from the running checks
type runnerCheckStats struct {
	Stats map[check.ID]*check.Stats
	M     sync.RWMutex
}

var (
	runnerStats *expvar.Map
	checkStats  *runnerCheckStats
)

func init() {
	runnerStats = expvar.NewMap("runner")
	runnerStats.Set("Checks", expvar.Func(expCheckStats))
	checkStats = &runnerCheckStats{
		Stats: make(map[check.ID]*check.Stats),
	}
}

// Runner ...
type Runner struct {
	pending          chan check.Check         // The channel where checks come from
	done             chan bool                // Guard for the main loop
	runningChecks    map[check.ID]check.Check // The list of checks running
	m                sync.Mutex               // To control races on runningChecks
	running          uint32                   // Flag to see if the Runner is, well, running
	staticNumWorkers bool                     // Flag indicating if numWorkers is dynamically updated
}

// NewRunner takes the number of desired goroutines processing incoming checks.
func NewRunner(numWorkers int) *Runner {
	r := &Runner{
		// initialize the channel
		pending:          make(chan check.Check),
		runningChecks:    make(map[check.ID]check.Check),
		running:          1,
		staticNumWorkers: numWorkers != 0, // numWorkers == 0 is the default value in the config file
	}

	if !r.staticNumWorkers {
		numWorkers = DEFAULT_NUM_WORKERS
	}

	// start the workers
	for i := 0; i < numWorkers; i++ {
		go r.work()
	}

	log.Infof("Runner started with %d workers.", numWorkers)
	runnerStats.Add("Workers", int64(numWorkers))
	return r
}

// UpdateNumWorkers checks if the current number of workers is reasonable, and adds more if needed
func (r *Runner) UpdateNumWorkers(numChecks int64) {
	numWorkers, _ := strconv.Atoi(runnerStats.Get("Workers").String())

	if r.staticNumWorkers || numWorkers >= MAX_NUM_WORKERS {
		return
	}

	if numChecks-int64(numWorkers) > 5 {
		// Add a worker
		runnerStats.Add("Workers", 1)
		log.Infof("Added worker to runner: now at " + runnerStats.Get("Workers").String() + " workers.")
		go r.work()
	}
}

// Stop closes the pending channel so all workers will exit their loop and terminate
func (r *Runner) Stop() {
	if atomic.LoadUint32(&r.running) == 0 {
		log.Debug("Runner already stopped, nothing to do here...")
		return
	}

	log.Info("Runner is shutting down...")

	close(r.pending)
	atomic.StoreUint32(&r.running, 0)

	// stop checks that are still running
	r.m.Lock()
	for _, check := range r.runningChecks {
		log.Infof("Stopping Check %v that is still running...", check)
		done := make(chan struct{})
		go func() {
			check.Stop()
			close(done)
		}()

		select {
		case <-done:
			// all good
		case <-time.After(stopCheckTimeout):
			// check is not responding
			log.Errorf("Check %v not responding, timing out...", check)
		}
	}
	r.m.Unlock()
}

// GetChan returns a write-only version of the pending channel
func (r *Runner) GetChan() chan<- check.Check {
	return r.pending
}

// StopCheck invokes the `Stop` method on a check if it's running. If the check
// is not running, this is a noop
func (r *Runner) StopCheck(id check.ID) error {
	done := make(chan bool)

	r.m.Lock()
	defer r.m.Unlock()

	if c, isRunning := r.runningChecks[id]; isRunning {
		log.Debugf("Stopping check %s", c)
		go func() {
			c.Stop()
			close(done)
		}()
	} else {
		return nil
	}

	select {
	case <-done:
		return nil
	case <-time.After(stopCheckTimeout):
		return fmt.Errorf("timeout during stop operation on check id %s", id)
	}
}

// work waits for checks and run them as long as they arrive on the channel
func (r *Runner) work() {
	log.Debug("Ready to process checks...")

	for check := range r.pending {
		// see if the check is already running
		r.m.Lock()
		if _, isRunning := r.runningChecks[check.ID()]; isRunning {
			log.Debugf("Check %s is already running, skip execution...", check)
			r.m.Unlock()
			continue
		} else {
			r.runningChecks[check.ID()] = check
			runnerStats.Add("RunningChecks", 1)
		}
		r.m.Unlock()

		log.Infof("Running check %s", check)

		// run the check
		t0 := time.Now()
		err := check.Run()
		warnings := check.GetWarnings()

		sender, e := aggregator.GetSender(check.ID())
		if e != nil {
			log.Debugf("Error getting sender: %v for check %s. trying to get default sender", e, check)
			sender, e = aggregator.GetDefaultSender()
			if e != nil {
				log.Errorf("Error getting default sender: %v. Not sending status check for %s", e, check)
			}
		}
		serviceCheckTags := []string{fmt.Sprintf("check:%s", check.String())}
		serviceCheckStatus := metrics.ServiceCheckOK

		hostname := getHostname()

		if len(warnings) != 0 {
			// len returns int, and this expect int64, so it has to be converted
			runnerStats.Add("Warnings", int64(len(warnings)))
			serviceCheckStatus = metrics.ServiceCheckWarning
		}

		if err != nil {
			log.Errorf("Error running check %s: %s", check, err)
			runnerStats.Add("Errors", 1)
			serviceCheckStatus = metrics.ServiceCheckCritical
		}

		if sender != nil {
			sender.ServiceCheck("datadog.agent.check_status", serviceCheckStatus, hostname, serviceCheckTags, "")
			sender.Commit()
		}

		// remove the check from the running list
		r.m.Lock()
		delete(r.runningChecks, check.ID())
		r.m.Unlock()

		// publish statistics about this run
		runnerStats.Add("RunningChecks", -1)
		runnerStats.Add("Runs", 1)
		addWorkStats(check, time.Since(t0), err, warnings)

		log.Infof("Done running check %s", check)
	}

	log.Debug("Finished processing checks.")
}

func addWorkStats(c check.Check, execTime time.Duration, err error, warnings []error) {
	var s *check.Stats
	var found bool

	checkStats.M.Lock()
	s, found = checkStats.Stats[c.ID()]
	if !found {
		s = check.NewStats(c)
		checkStats.Stats[c.ID()] = s
	}
	checkStats.M.Unlock()

	s.Add(execTime, err, warnings)
}

func expCheckStats() interface{} {
	checkStats.M.RLock()
	defer checkStats.M.RUnlock()

	return checkStats.Stats
}

// GetCheckStats returns the check stats map
func GetCheckStats() map[check.ID]*check.Stats {
	checkStats.M.RLock()
	defer checkStats.M.RUnlock()

	return checkStats.Stats
}

func getHostname() string {
	hname, found := util.Cache.Get(path.Join(util.AgentCachePrefix, "hostname"))
	if found {
		return hname.(string)
	}
	return ""
}
