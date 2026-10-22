package personalassistant

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// WorkspaceGoalMaxRunes bounds a workspace goal a person types: the build
// session's goal and a folder setup's brief share it.
const WorkspaceGoalMaxRunes = 300

// How a folder setup staffs the folder's project, as its card says it.
const (
	// FolderHireCreate makes a new agent from the blueprint's roster.
	FolderHireCreate = "create"
	// FolderHireBind assigns an agent the user already has: the Home's shared
	// assistant (hire once, assign many) or a saved agent of the same name.
	FolderHireBind = "bind"
	// FolderHirePending is a blueprint whose plugin is not installed yet, so
	// its roster cannot be read; the card names the capability instead.
	FolderHirePending = "pending"
)

// FolderHire is who a folder setup hires for the folder, as the card names it.
// It is read fresh for each plan and is not part of the digest: the plan's
// lines already say what the run will create, and these are the words the card
// leads with. Every field is plain text and none is a path.
type FolderHire struct {
	Mode string `json:"mode"`
	// AgentName is the agent the run creates or assigns; empty while pending.
	AgentName string `json:"agent_name,omitempty"`
	// RoleLabel is the role the blueprint gives it, or the capability's label
	// while the blueprint cannot be read.
	RoleLabel string `json:"role_label,omitempty"`
	// Model is the model the agent is set to use; empty when it has none yet.
	Model string `json:"model,omitempty"`
}

// validate keeps a hire bounded and path-free.
func (h *FolderHire) validate() error {
	if h == nil {
		return nil
	}
	switch h.Mode {
	case FolderHireCreate, FolderHireBind, FolderHirePending:
	default:
		return fmt.Errorf("%w: hire mode", errFolderDigestInvalid)
	}
	for _, text := range []string{h.AgentName, h.RoleLabel, h.Model} {
		if len(text) > 200 || strings.ContainsAny(text, "\x00\r\n") || looksLikeFilesystemPath(text) {
			return fmt.Errorf("%w: hire text", errFolderDigestInvalid)
		}
	}
	return nil
}

// folderDefaultBriefs is what a hired agent is asked to do with a folder of
// each shape when its blueprint declares no goal of its own.
var folderDefaultBriefs = map[folderdigest.Shape]string{
	folderdigest.ShapeCode:       "Understand this project, find evidence-backed risks, and propose improvements before anything changes.",
	folderdigest.ShapeManuscript: "Understand this manuscript's structure and state, and suggest the next thing to write or fix.",
	folderdigest.ShapeNotes:      "Learn how these notes are organized and suggest one way to make them more useful.",
	folderdigest.ShapeCorpus:     "Catalogue what this collection contains and suggest the first thing worth doing with it.",
	folderdigest.ShapeAudio:      "Understand this song's project and suggest the next production step.",
}

// FolderDefaultBrief is the brief a hire card starts from: the blueprint's own
// goal when it declares one, else the host's sentence for the folder's shape.
func FolderDefaultBrief(shape folderdigest.Shape, blueprintGoal string) string {
	if goal := strings.Join(strings.Fields(blueprintGoal), " "); goal != "" {
		return goal
	}
	return folderDefaultBriefs[shape]
}

// NormalizeFolderBrief is the brief as it is stored and becomes the workspace
// goal: one line of the user's own words, bounded like the build session's
// goal, with no control characters and nothing that looks like a secret.
// Empty stays empty; the caller falls back to the plan's default.
func NormalizeFolderBrief(text string) (string, error) {
	brief, err := validateText("brief", strings.Join(strings.Fields(text), " "), WorkspaceGoalMaxRunes, false)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrValidation, err.Error())
	}
	return brief, nil
}
