//go:build !darwin
// +build !darwin

package main

import "log"

func main() {
	log.Println("Ori Agent menubar helper is only supported on macOS; skipping launch.")
}
