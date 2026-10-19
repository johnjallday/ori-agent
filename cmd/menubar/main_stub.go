//go:build !darwin
// +build !darwin

package main

import (
	"log"
	"os"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
)

func main() {
	if handled, code := backuprecovery.RunHelperProcess(os.Args[1:]); handled {
		os.Exit(code)
	}
	log.Println("Ori Agent menubar helper is only supported on macOS; skipping launch.")
}
