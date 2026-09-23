//go:build !linux

package main

import "os/exec"

// lifeline does nothing where the kernel has no signal for the death of a parent: on macOS the
// cleanups of the tests are all that ends a factory they started.
func lifeline(*exec.Cmd) {}
