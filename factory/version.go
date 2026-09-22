package main

import (
	_ "embed"
	"strings"
)

// The factory's version lives in VERSION and nowhere else: the binary embeds that file, reports it
// on -version and records it with every run, and scripts/release.sh tags the release from it. So
// the binary a host runs, the run it recorded and the tag it was built from cannot disagree.
//
//go:embed VERSION
var versionFile string

// version is what VERSION says, without the newline it ends in.
var version = strings.TrimSpace(versionFile)
