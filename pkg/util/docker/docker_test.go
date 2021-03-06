// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2017 Datadog, Inc.

// +build docker

package docker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// detab removes whitespace from the front of a string on every line
func detab(str string) string {
	detabbed := make([]string, 0)
	for _, l := range strings.Split(str, "\n") {
		s := strings.TrimSpace(l)
		if len(s) > 0 {
			detabbed = append(detabbed, s)
		}
	}
	return strings.Join(detabbed, "\n")
}

// Sanity-check that all containers works with different settings.
func TestAllContainers(t *testing.T) {
	InitDockerUtil(&Config{CollectNetwork: true})
	AllContainers(&ContainerListConfig{IncludeExited: false, FlagExcluded: true})
	AllContainers(&ContainerListConfig{IncludeExited: true, FlagExcluded: true})
	InitDockerUtil(&Config{CollectNetwork: false})
	AllContainers(&ContainerListConfig{IncludeExited: false, FlagExcluded: true})
	AllContainers(&ContainerListConfig{IncludeExited: true, FlagExcluded: true})
}

func TestParseContainerHealth(t *testing.T) {
	assert := assert.New(t)
	for i, tc := range []struct {
		input    string
		expected string
	}{
		{
			input:    "",
			expected: "",
		},
		{
			input:    "Up 2 minutes",
			expected: "",
		},
		{
			input:    "Up about 1 hour (health: starting)",
			expected: "starting",
		},
		{
			input:    "Up 1 minute (health: unhealthy)",
			expected: "unhealthy",
		},
	} {
		assert.Equal(tc.expected, parseContainerHealth(tc.input), "test %d failed", i)
	}
}

func TestExtractImageName(t *testing.T) {
	imageName := "datadog/docker-dd-agent:latest"
	imageSha := "sha256:bdc7dc8ba08c2ac8c8e03550d8ebf3297a669a3f03e36c377b9515f08c1b4ef4"
	imageWithShaTag := "datadog/docker-dd-agent@sha256:9aab42bf6a2a068b797fe7d91a5d8d915b10dbbc3d6f2b10492848debfba6044"

	assert := assert.New(t)
	globalDockerUtil = &dockerUtil{
		cfg:            &Config{CollectNetwork: false},
		cli:            nil,
		imageNameBySha: make(map[string]string),
	}
	globalDockerUtil.imageNameBySha[imageWithShaTag] = imageName
	globalDockerUtil.imageNameBySha[imageSha] = imageName
	for i, tc := range []struct {
		input    string
		expected string
	}{
		{
			input:    "",
			expected: "",
		}, {
			input:    imageName,
			expected: imageName,
		}, {
			input:    imageWithShaTag,
			expected: imageName,
		}, {
			input:    imageSha,
			expected: imageName,
		},
	} {
		assert.Equal(tc.expected, globalDockerUtil.extractImageName(tc.input), "test %s failed", i)
	}
}
