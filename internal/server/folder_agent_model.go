package server

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/folderlook"
)

func (t folderAgentTurns) Model(_ context.Context, workspaceID, agentName string) (folderlook.Chat, folderlook.ModelRef, error) {
	chat, ref, err := t.builder.agentToolModel(workspaceID, agentName)
	if err != nil {
		return nil, ref, err
	}
	if chat == nil {
		return nil, ref, folderlook.ErrNoModel
	}
	return folderlook.Chat(chat), ref, nil
}
