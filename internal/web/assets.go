package web

import (
	"embed"
)

// Embed the entire static directory.
//
//go:embed static/*
var Static embed.FS
