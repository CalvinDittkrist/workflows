//go:build !race

package main

// raceEnabled says whether this build has the race detector, so the tests build the factory they
// start the same way they were built themselves.
const raceEnabled = false
