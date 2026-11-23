//go:build !darwin
// +build !darwin

package main

import (
	"log"
	"os"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/fileparser"
)

func main() {
	if handled, code := fileparser.RunReviewPDFWorker(os.Args[1:]); handled {
		os.Exit(code)
	}
	if handled, code := backuprecovery.RunHelperProcess(os.Args[1:]); handled {
		os.Exit(code)
	}
	log.Println("Ori Agent menubar helper is only supported on macOS; skipping launch.")
}
