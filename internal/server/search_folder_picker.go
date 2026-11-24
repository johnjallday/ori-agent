package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/platform"
)

func pickSearchFolder(ctx context.Context) (string, bool, error) {
	return platform.ChooseFolder(ctx, "Choose one folder for local filename search")
}
