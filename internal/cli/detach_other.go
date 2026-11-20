//go:build !unix

package cli

import "syscall"

// detachedAttr is nothing here: the process is started and not waited for,
// and whether it outlives its parent is the system's rule.
func detachedAttr() *syscall.SysProcAttr { return nil }
