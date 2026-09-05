package main

// Everything in this file exists because the FIRST attempt at this benchmark
// (agent-estate#1002) was told in prose to run one worker at a time and to
// stop below a memory floor, and did neither. Two benchmark binaries ran
// concurrently, the larger grew from 889MB to 1753MB, the host's swap file
// went 1024MB -> 29696MB, and the operator stopped the run by hand.
//
// A runaway cannot police itself. So the two rules are mechanisms here, not
// sentences: `runLock` makes a second benchmark process on this host fail to
// start, and `serial` makes a second concurrent turn inside one process
// return an error instead of running. Both are exercised by guard_test.go --
// the point is not that they are written down, it is that removing either one
// fails a test.

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
)

// errConcurrentTurn is what a second concurrent turn gets instead of a turn.
var errConcurrentTurn = errors.New("a second turn tried to start while one was already running; this benchmark runs strictly one at a time")

// serial admits exactly one Do at a time. A second concurrent caller is
// REFUSED rather than queued: queueing would let a caller that should not
// exist run a moment later, which is the same runaway one tick delayed.
//
// The run loop in main.go is already a plain sequential for-loop, so in a
// correct build this never fires. That is the point -- it is the assertion
// that catches the refactor which introduces a goroutine, at the moment it
// would otherwise put two agent processes on the host at once.
type serial struct{ n atomic.Int32 }

func (s *serial) Do(f func() error) error {
	if s.n.Add(1) != 1 {
		s.n.Add(-1)
		return errConcurrentTurn
	}
	defer s.n.Add(-1)
	return f()
}

// runLock is an exclusive claim, held on the filesystem, on the right to run
// this benchmark on this host. Process-level, because `serial` cannot see a
// second COPY of the binary -- which is exactly what the first attempt ran.
type runLock struct{ path string }

// acquireRunLock takes the lock or reports who holds it. A lock file whose
// recorded pid is no longer alive is stale and is broken once; a lock file
// whose pid IS alive is refused, with the pid named so the operator can look
// at the process rather than guess.
//
// Deliberately not lockf/flock: a stale advisory lock vanishes when the
// holder dies, which is convenient and also means a crashed run leaves no
// trace to look at. A pid file that must be reasoned about is the honest
// artifact -- and reasoning about it is `kill(pid, 0)`, not a timestamp.
func acquireRunLock(path string) (*runLock, error) {
	take := func() error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, "%d\n", os.Getpid())
		return err
	}

	err := take()
	if err == nil {
		return &runLock{path: path}, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("could not take the benchmark run lock at %s: %w", path, err)
	}

	holder, readErr := lockHolder(path)
	if readErr != nil {
		// An unreadable lock file is not permission to run. Blindness is not
		// capacity -- the same disposition internal/pressure applies to a
		// gauge it cannot read.
		return nil, fmt.Errorf("a benchmark run lock exists at %s and cannot be read (%v); refusing rather than assuming it is stale", path, readErr)
	}
	if processAlive(holder) {
		return nil, fmt.Errorf("another benchmark is already running on this host (pid %d, lock %s); this benchmark runs one at a time", holder, path)
	}
	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("stale run lock at %s (pid %d is gone) could not be removed: %w", path, holder, err)
	}
	if err := take(); err != nil {
		return nil, fmt.Errorf("could not take the benchmark run lock at %s after clearing a stale one: %w", path, err)
	}
	return &runLock{path: path}, nil
}

func (l *runLock) Release() error {
	if l == nil {
		return nil
	}
	return os.Remove(l.path)
}

func lockHolder(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("lock file does not contain a pid: %q", strings.TrimSpace(string(b)))
	}
	if pid <= 0 {
		return 0, fmt.Errorf("lock file contains a nonsense pid: %d", pid)
	}
	return pid, nil
}

// processAlive reports whether pid exists. Signal 0 performs the permission
// and existence checks without delivering anything. EPERM means the process
// exists and belongs to somebody else -- alive, and emphatically not ours to
// clear.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
