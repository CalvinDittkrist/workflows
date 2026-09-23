package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// lifeline has the kernel kill the process with the one that started it. The signal is tied to the
// thread that forks it, and the Go runtime ends none of its threads unless a goroutine locked to one
// ends, which no test does.
func lifeline(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// orphanHelper is set in the environment of the test process TestNoFactoryOutlivesTheTestProcess
// runs, and makes TestAFactoryWhoseTestNeverEnds the test that starts a factory and hangs.
const orphanHelper = "FACTORY_TEST_ORPHAN"

// A test process that ends without its cleanups takes the factories it started with it, however it
// ends: killed from outside, as a tool call that runs out of time kills it, or ended by go test
// itself on its -timeout. The test process here is this test binary run again on the test below,
// which starts a factory and hangs; the factory is read from /proc, not from anything it says.
func TestNoFactoryOutlivesTheTestProcess(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		timeout string // the -test.timeout of the test process
		kill    bool   // the test process is killed rather than left to its timeout
	}{
		{name: "killed", timeout: "10m", kill: true},
		{name: "timed out", timeout: "20s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			// Its temporary directories are made in one of this test's, so what it leaves is removed.
			env := append(os.Environ(), orphanHelper+"=1", builtBinaries+"="+filepath.Dir(binary), "TMPDIR="+t.TempDir())
			cmd := exec.Command(os.Args[0], "-test.run=^TestAFactoryWhoseTestNeverEnds$", "-test.timeout="+c.timeout)
			cmd.Env = env
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			// Its error output and the lines read from its standard output are written from two
			// goroutines, exec's copy and the reader below.
			said := &saying{}
			cmd.Stderr = said
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ended := make(chan struct{})
			pid := 0
			go func() {
				defer close(ended)
				lines := bufio.NewScanner(stdout)
				for lines.Scan() {
					_, _ = said.Write([]byte(lines.Text() + "\n"))
					if n, found := strings.CutPrefix(lines.Text(), "factory pid "); found && pid == 0 {
						pid, _ = strconv.Atoi(n)
						if c.kill {
							_ = cmd.Process.Signal(syscall.SIGKILL)
						}
					}
				}
				_ = cmd.Wait()
			}()
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			select {
			case <-ended:
			case <-time.After(2 * time.Minute):
				t.Fatalf("the test process did not end; it said:\n%s", said.String())
			}
			if pid == 0 {
				t.Fatalf("the test process ended before it started a factory; it said:\n%s", said.String())
			}
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			for deadline := time.Now().Add(time.Second); running(pid); {
				if time.Now().After(deadline) {
					t.Fatalf("the factory %d outlived the test process that started it by a second", pid)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

// TestAFactoryWhoseTestNeverEnds is the test process of the test above: it starts a factory, says
// which, and waits for the end the test above gives it. Run by itself, it does nothing.
func TestAFactoryWhoseTestNeverEnds(t *testing.T) {
	if os.Getenv(orphanHelper) == "" {
		t.Skip("the test process of TestNoFactoryOutlivesTheTestProcess")
	}
	f := start(t, config{"paused": true})
	if !running(f.cmd.Process.Pid) {
		t.Fatalf("the factory %d is not running after it answered", f.cmd.Process.Pid)
	}
	fmt.Printf("factory pid %d\n", f.cmd.Process.Pid)
	time.Sleep(time.Hour)
}

// saying is what the test process said, written to from more than one goroutine.
type saying struct {
	mu  sync.Mutex
	out strings.Builder
}

func (s *saying) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.Write(p)
}

func (s *saying) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// running says whether /proc has the process of that id and it is not a zombie, which is what an
// ended process is until its parent reaps it.
func running(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// The state follows the command name, which is in parentheses and may hold any of them.
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z"
}
