//go:build !android && !ios && !server && !windows

package main

import (
	"syscall"
	"time"
)

// waitForExit waits (up to 15 s) until a process has exited.
func waitForExit(pid int) {
	for i := 0; i < 150 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
}
