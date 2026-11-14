package folderagent

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/agentappearance"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// AgentNameMaxRunes bounds a hired agent's name on the card.
const AgentNameMaxRunes = 60

// knownFolderHire provides editable defaults before the hire. Identity comes
// from the host's disk comparison, never the folder's name or a browser flag.
func knownFolderHire(policy folderlook.RootPolicy) (purpose, brief string) {
	switch policy {
	case folderlook.RootDownloads:
		return "Downloads is an inbox. This agent sorts what lands here, and you approve every move.", "Keep Downloads sorted"
	case folderlook.RootDesktop:
		return "Desktop is a workbench. This agent files screenshots and loose files by kind, and you approve every move.", "Keep the Desktop clear"
	case folderlook.RootDocuments:
		return "Documents is where you keep things. This agent points out the projects in it and whether Ori can see a backup.", "Show me what I keep here"
	default:
		return "", DefaultBrief
	}
}

// PreferredCharacter is a presentation hint, never a role or a permission.
// Keep the folder defaults together with knownFolderHire.
func PreferredCharacter(policy folderlook.RootPolicy) string {
	switch policy {
	case folderlook.RootDownloads:
		return "operations-keeper"
	case folderlook.RootDesktop:
		return "automation-specialist"
	case folderlook.RootDocuments:
		return "research-archivist"
	default:
		return "project-coordinator"
	}
}

// HireModel is one model the card offers for a new agent.
type HireModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Label    string `json:"label"`
	// Default marks the system model, preselected on the card. With none
	// marked the card preselects no model, as an agent hired without one.
	Default bool `json:"default,omitempty"`
}

// HireAgent is one of the owner's own agents the card offers to reuse.
type HireAgent struct {
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
}

// HireOptions is the card's Hire section: who would be hired, and the
// choices. Nothing is created until Hire & build.
type HireOptions struct {
	// AgentName is the new agent's prefilled name ("<folder> Manager").
	AgentName string `json:"agent_name"`
	// Mandate is the new agent's prefilled one-line mandate; empty means the
	// brief is used.
	Mandate  string      `json:"mandate"`
	Provider string      `json:"provider,omitempty"`
	Model    string      `json:"model,omitempty"`
	Models   []HireModel `json:"models"`
	Agents   []HireAgent `json:"agents"`
	// Existing preselects one of Agents (a saved "<folder> Manager" is reused
	// rather than hired twice).
	Existing  string `json:"existing,omitempty"`
	NameMax   int    `json:"name_max"`
	Character string `json:"character,omitempty"`
}

// Hire is the owner's choice on the card: one of their agents (Existing), or
// a new agent with a name, a model and a mandate.
type Hire struct {
	Existing  string
	Name      string
	Provider  string
	Model     string
	Mandate   string
	Character string
}

// Hiring is the server side of the Hire section.
type Hiring interface {
	// Models lists the models a new agent can have, the default first.
	Models(ctx context.Context) []HireModel
	// Agents lists the owner's own agents (never the built-in assistant).
	Agents(ctx context.Context, userID string) []HireAgent
	// AgentExists reports any agent of that name, compared case-insensitively.
	AgentExists(name string) bool
	// Character suggests a least-worn working character for this folder kind.
	Character(ctx context.Context, userID string, policy folderlook.RootPolicy) string
}

func (s *Service) hireOptions(ctx context.Context, userID, folder string) *HireOptions {
	out := &HireOptions{AgentName: folder + " Manager", Models: []HireModel{}, Agents: []HireAgent{}, NameMax: AgentNameMaxRunes}
	if s.deps.Hiring == nil {
		return out
	}
	out.Models = append(out.Models, s.deps.Hiring.Models(ctx)...)
	for _, m := range out.Models {
		if m.Default {
			out.Provider, out.Model = m.Provider, m.Model
			break
		}
	}
	out.Agents = append(out.Agents, s.deps.Hiring.Agents(ctx, userID)...)
	for _, agent := range out.Agents {
		if strings.EqualFold(agent.Name, out.AgentName) {
			out.Existing = agent.Name
		}
	}
	return out
}

// resolveHire checks the owner's choice against what the card offered and
// returns the creation's agent fields. It reads nothing from the folder.
func (s *Service) resolveHire(ctx context.Context, userID, folder, brief string, hire Hire) (CreateRequest, error) {
	options := s.hireOptions(ctx, userID, folder)
	if existing := strings.TrimSpace(hire.Existing); existing != "" {
		for _, agent := range options.Agents {
			if strings.EqualFold(agent.Name, existing) {
				return CreateRequest{AgentName: agent.Name, BindExisting: true}, nil
			}
		}
		return CreateRequest{}, &Refusal{Code: "agent_unknown", Status: http.StatusBadRequest,
			Message: "That agent is not one of yours. Choose another or hire a new one."}
	}
	name := strings.Join(strings.Fields(hire.Name), " ")
	if name == "" {
		name = options.AgentName
	}
	if utf8.RuneCountInString(name) > AgentNameMaxRunes || strings.ContainsAny(name, `/\:`) || strings.ContainsAny(hire.Name, "\r\n") {
		return CreateRequest{}, &Refusal{Code: "agent_name_invalid", Status: http.StatusBadRequest,
			Message: "Give the agent a one-line name under 60 characters, without slashes or colons."}
	}
	if s.deps.Hiring != nil && s.deps.Hiring.AgentExists(name) {
		return CreateRequest{}, &Refusal{Code: "agent_name_taken", Status: http.StatusConflict,
			Message: "You already have an agent named " + name + ". Choose it under One of your agents, or give this one another name."}
	}
	// The card always sends the model it shows; an empty model is "No model
	// yet", hired without one.
	req := CreateRequest{AgentName: name}
	if id := strings.TrimSpace(hire.Character); id != "" {
		version, err := agentappearance.ResolveAssignableCharacter(id)
		if err != nil {
			return CreateRequest{}, &Refusal{Code: "agent_character_unknown", Status: http.StatusBadRequest,
				Message: "That face is not available here. Choose another."}
		}
		req.Character, req.CharacterVersion = id, version
	}
	if model := strings.TrimSpace(hire.Model); model != "" {
		found := false
		for _, offered := range options.Models {
			if offered.Model == model && (hire.Provider == "" || strings.EqualFold(offered.Provider, strings.TrimSpace(hire.Provider))) {
				req.Provider, req.Model, found = offered.Provider, offered.Model, true
				break
			}
		}
		if !found {
			return CreateRequest{}, &Refusal{Code: "agent_model_unknown", Status: http.StatusBadRequest,
				Message: "That model is not available here. Choose one from the list."}
		}
	}
	mandate, err := personalassistant.NormalizeFolderBrief(hire.Mandate)
	if err != nil {
		return CreateRequest{}, err
	}
	if mandate == "" {
		mandate = brief
	}
	req.Mandate = mandate
	return req, nil
}
