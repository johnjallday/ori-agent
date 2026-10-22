package personalassistant

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/types"
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

// roleDefaultBriefs is what an agent hired from the Agents page is asked to do
// when the user leaves "What should it do?" as it was, by catalog role.
var roleDefaultBriefs = map[types.AgentRole]string{
	types.RoleOrchestrator: "Take requests for this workspace and route them to the right work.",
	types.RoleResearcher:   "Find and summarize what's known about this topic, with sources.",
	types.RoleAnalyzer:     "Explain what's going on in the material you're given, with your reasoning shown.",
	types.RoleSynthesizer:  "Turn the notes and findings here into a clear draft.",
	types.RoleValidator:    "Check the work here for mistakes and flag problems before they ship.",
	types.RoleSpecialist:   "Act as the expert this workspace needs.",
}

// hireDomainBrief is a Specialist's default brief once it has a domain.
const hireDomainBrief = "Act as the {domain} expert for this workspace."

// HireDefaultBrief is the brief a hire card starts from for a catalog role.
// A Specialist with a domain is that domain's expert. Empty for a role the
// catalog does not have.
func HireDefaultBrief(role types.AgentRole, domain string) string {
	if domain = strings.Join(strings.Fields(domain), " "); role == types.RoleSpecialist && domain != "" {
		return truncateRunes(strings.Replace(hireDomainBrief, "{domain}", domain, 1), WorkspaceGoalMaxRunes)
	}
	return roleDefaultBriefs[role]
}

// HireDomainBrief is the Specialist's domain brief with a "{domain}"
// placeholder, for a card that fills it in as the user types.
func HireDomainBrief() string { return hireDomainBrief }

// HireWorkspaceNameMaxRunes bounds the name of a workspace a hire creates.
const HireWorkspaceNameMaxRunes = 80

// hireNameWords is how many of the brief's first words name its workspace.
const hireNameWords = 5

// hireNameSmallWords stay lower case inside a name and never end one.
var hireNameSmallWords = map[string]bool{
	"a": true, "about": true, "an": true, "and": true, "as": true, "at": true, "by": true, "for": true,
	"from": true, "in": true, "into": true, "of": true, "on": true, "or": true, "the": true, "to": true, "with": true,
}

// HireWorkspaceName is the workspace name a hire derives from its brief: the
// brief's first five words in title case, small words kept lower case and
// dropped from the end, punctuation around words removed. The Agents page's
// card prefills the same name (agents.js hireWorkspaceName); keep them equal.
// Empty when the brief has no words.
func HireWorkspaceName(brief string) string {
	var words []string
	for _, field := range strings.Fields(brief) {
		word := strings.TrimFunc(field, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if word == "" {
			continue
		}
		words = append(words, word)
		if len(words) == hireNameWords {
			break
		}
	}
	for len(words) > 0 && hireNameSmallWords[strings.ToLower(words[len(words)-1])] {
		words = words[:len(words)-1]
	}
	for i, word := range words {
		if i > 0 && hireNameSmallWords[strings.ToLower(word)] {
			words[i] = strings.ToLower(word)
			continue
		}
		first, size := utf8.DecodeRuneInString(word)
		words[i] = string(unicode.ToUpper(first)) + word[size:]
	}
	name := strings.Join(words, " ")
	for utf8.RuneCountInString(name) > HireWorkspaceNameMaxRunes {
		cut := strings.LastIndex(name, " ")
		if cut <= 0 {
			return truncateRunes(name, HireWorkspaceNameMaxRunes)
		}
		name = name[:cut]
	}
	return name
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
