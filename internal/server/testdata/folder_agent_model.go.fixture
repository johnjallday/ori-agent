// Only in the search-first fixture binary: real walk/report, no provider or CLI turn.
package server

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/folderlook"
)

func (folderAgentTurns) Model(context.Context, string, string) (folderlook.Chat, folderlook.ModelRef, error) {
	return nil, folderlook.ModelRef{}, folderlook.ErrNoModel
}
