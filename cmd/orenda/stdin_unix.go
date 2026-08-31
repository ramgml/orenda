//go:build !windows

package main

import "syscall"

// stdinFD returns the standard-input file descriptor on Unix-likes.
func stdinFD() int { return syscall.Stdin }
