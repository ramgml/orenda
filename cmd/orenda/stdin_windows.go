//go:build windows

package main

import "syscall"

// stdinFD returns the standard-input handle on Windows, where
// syscall.Stdin is a Handle (uintptr) rather than an int fd.
func stdinFD() int { return int(syscall.Stdin) }
