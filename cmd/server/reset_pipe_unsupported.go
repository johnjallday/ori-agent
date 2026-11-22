//go:build !darwin && !linux

package main

func markResetPipeCloseOnExec() {} // Managed reset exits before opening a pipe here.
