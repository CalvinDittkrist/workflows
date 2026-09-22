package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// The data directory is the one thing two factories on a host cannot share: the run ids, the run
// records, the clones and the worktrees in it are written as if this process were alone with them.
// The address the interface answers on is no guard for that — two configurations that name two
// addresses would both start, work two issues at once and race each other's records — so the
// directory itself is taken, before anything is read of it or written to it ([ADR 0027]).
//
// The lock is the kernel's, held by the open file and released when the process is gone whatever
// ended it: a host that lost power finds no lock of its own to clear away, which matters where
// nobody is watching.
//
// [ADR 0027]: ../docs/adr/0027-the-factorys-isolation-boundary-is-the-host.md

// lockFile is the file in the data directory the lock is held on. It is not read: its existence is
// nothing, and holding it is everything.
const lockFile = "factory.lock"

// lockDataDir takes the data directory for this factory and answers with the release of it.
func lockDataDir(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the data directory %s cannot be created: %w; name a writable data_dir", dir, err)
	}
	path := filepath.Join(dir, lockFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the lock %s cannot be opened: %w; name a writable data_dir", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("the data directory %s is held by another factory: %w; one factory works one data directory, whatever address it answers on", dir, err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}
