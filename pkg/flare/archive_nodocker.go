// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2017 Datadog, Inc.

// +build !docker

package flare

import (
	"github.com/jhoonb/archivex"
	"path"
	"path/filepath"
	"strings"
)

func zipDockerSelfInspect(zipFile *archivex.ZipFile, hostname string) error {
	return nil
}
