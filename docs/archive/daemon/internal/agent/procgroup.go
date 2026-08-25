package agent

import (
	"os/exec"
	"syscall"
)

// STOLEN FROM: paperclip packages/adapter-utils/src/server-utils.ts:3341 --
// `detached: true, shell: false`, and the pid AND process-group id persisted
// at spawn, BEFORE the first read.
//
// Why it matters here: an agent spawns children (git, gh, node, test runners).
// Killing the parent pid orphans them, and orphaned test processes are exactly
// what was found on this machine tonight -- three `jest --runInBand` runs, 12
// days 21 hours old, holding 1.89GB while the operator's Mac swapped. Setpgid
// makes the whole tree one signalable group.
func setProcGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup signals the whole process group. Negative pid = the group.
func killGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return nil
	}
	return syscall.Kill(-pgid, sig)
}

// Alive reports whether a pid is still live. Used by the reaper.
//
// NOTE the trap this daemon must not repeat: `syscall.Kill(pid,0)` answers
// "some process holds this pid", not "MY process holds it". A zombie
// (<defunct>) also answers yes -- which is exactly how a dead supervisor lease
// holder read as ALIVE earlier tonight and blocked four dispatches. Callers
// must corroborate with the recorded start time, not trust this alone.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
