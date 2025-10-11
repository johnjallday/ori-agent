#!/bin/bash

# Dolphin Desktop Launcher
# Double-click this file to run the desktop app

cd "$(dirname "$0")"
CGO_LDFLAGS="-framework UniformTypeIdentifiers" go run -tags dev cmd/wails/main.go
