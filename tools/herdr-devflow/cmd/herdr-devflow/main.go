package main

import (
	"context"
	"os"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/app"
)

func main() {
	application := app.New(app.Dependencies{})
	os.Exit(application.Run(context.Background(), os.Args[1:]))
}
