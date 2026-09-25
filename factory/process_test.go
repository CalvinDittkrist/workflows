package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every factory a test starts is bound to the life of the test process. The cleanup a test registers
// ends it when the test ends, but go test can end first (on its -timeout, on a panic, or when
// whatever ran it is killed), and then no cleanup runs: a factory left like that polls its gh shim
// for as long as the host is up. So the binary is started here and nowhere else, and on Linux, where
// every host runs, the kernel kills it with the test process (lifeline). The cleanups stay, because
// they end a factory the way a stop does, and on macOS they are all there is.

// builtBinaries names the directory of a factory that is already built, for a test process that runs
// this test binary again: it then starts that one rather than building its own.
const builtBinaries = "FACTORY_TEST_BINARIES"

// factoryCommand is the command that starts the binary bin of a test.
func factoryCommand(bin string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	lifeline(cmd)
	return cmd
}

// factoryCommandContext is factoryCommand under a context that kills it.
func factoryCommandContext(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	lifeline(cmd)
	return cmd
}

// A start of the binary that does not go through factoryCommand is one the kernel does not end with
// the test process, so it is refused here by its spelling: the binaries of the tests are named
// binary, hurried or bin wherever they are started.
func TestNoTestSpellsAStartOfTheBinaryOutsideTheHelper(t *testing.T) {
	t.Parallel()
	direct := regexp.MustCompile(`exec\.Command(Context\([^,]+,)?\(?\s*(binary|hurried|bin)\b`)
	sources, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source == "process_test.go" {
			continue
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(raw), "\n") {
			if direct.MatchString(line) {
				t.Errorf("%s:%d starts the factory with exec itself; start it with factoryCommand, which ends it with the test process", source, n+1)
			}
		}
	}
}
