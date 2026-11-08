package folderagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// lookTool is folder_look as a workspace tool (PRD FR13): the folder's agent
// can open one more folder of the map at any time. It reads through the
// workspace's own anchored folder, never a path a client or model supplies,
// and writes what it lists into the same map.
type lookTool struct {
	service     *Service
	workspaceID string
}

// LookTool returns folder_look for a workspace started from a folder, or nil.
func (s *Service) LookTool(workspaceID string) toolapi.Tool {
	if s == nil || s.deps.Store == nil || s.deps.Workspaces == nil {
		return nil
	}
	if _, err := s.deps.Store.Read(workspaceID); err != nil {
		return nil
	}
	return lookTool{service: s, workspaceID: workspaceID}
}

func (t lookTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
		Name: folderlook.LookToolName,
		Description: "List one folder of this workspace's folder by its path relative to the folder, as the folder map " +
			"writes it (\"\" is the folder itself). Returns names, kinds and Ori's project markers, never file contents; " +
			"at most 500 entries. Only folders the map already lists can be opened.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"folder": map[string]any{"type": "string", "description": "The folder's path relative to the workspace's folder."},
			},
			"required":             []string{"folder"},
			"additionalProperties": false,
		},
	}
}

func (t lookTool) Call(_ context.Context, args string) (string, error) {
	var in struct {
		Folder *string `json:"folder"`
	}
	decoder := json.NewDecoder(strings.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil || in.Folder == nil {
		return "", errors.New(`give one "folder", as the map writes it`)
	}
	if t.service.Running(t.workspaceID) {
		return "", errors.New("the folder is still being looked at; try again in a moment")
	}
	root, err := t.service.deps.Workspaces.AnchorRoot(t.workspaceID)
	if err != nil {
		return "", errors.New("the folder is no longer where it was")
	}
	var result foldermap.LookResult
	_, err = t.service.deps.Store.Update(t.workspaceID, func(r *Record) error {
		if r.Map == nil {
			return foldermap.ErrNotInMap
		}
		looked, err := foldermap.Look(root, r.Map, *in.Folder)
		if err != nil {
			return err
		}
		result = looked
		r.Facts = foldermap.FactsFrom(r.Map)
		r.Stage = foldermap.StageFor(r.Facts)
		return nil
	})
	if errors.Is(err, foldermap.ErrNotInMap) {
		return "", errors.New("that folder is not in the map; open one the map lists")
	}
	if err != nil {
		return "", errors.New("that folder could not be listed")
	}
	raw, err := json.Marshal(result)
	return string(raw), err
}
