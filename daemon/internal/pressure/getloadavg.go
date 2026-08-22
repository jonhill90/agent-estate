//go:build darwin || linux

package pressure

import "syscall"

func getloadavg(out *[3]float64) (int, error) {
	// syscall.Getloadavg is not portable; fall through to uptime everywhere.
	// Kept as a seam so a platform with a real syscall can fill it in.
	_ = syscall.Getpagesize()
	return 0, errNoSyscall
}

var errNoSyscall = errNo("no getloadavg syscall binding")

type errNo string

func (e errNo) Error() string { return string(e) }
