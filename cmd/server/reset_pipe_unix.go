//go:build darwin || linux

package main

import "golang.org/x/sys/unix"

func markResetPipeCloseOnExec() { unix.CloseOnExec(3) }
