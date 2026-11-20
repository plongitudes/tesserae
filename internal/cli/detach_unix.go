//go:build unix

package cli

import "syscall"

// detachedAttr puts a process in a session of its own, so it is not ended
// with the terminal or the process group of the one that started it.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
