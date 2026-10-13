//go:build windows && !server

package main

import "golang.org/x/sys/windows"

// waitForExit waits (up to 15 s) until a process has exited.
func waitForExit(pid int) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, 15000)
}
