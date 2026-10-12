package agenthttp

import (
	"context"
	"strings"
)

// FolderPathFound is a folder the user's own message points at by its path.
type FolderPathFound struct {
	// Path is the checked, canonical folder; Name its base name.
	Path, Name string
	// Text is the path as the user wrote it, and Start and End its byte range
	// in the message.
	Text       string
	Start, End int
	// WarningNote is a sentence the card shows before the user confirms (the
	// folder is outside their home folder); "" for none.
	Warning, WarningNote string
	// Refusal is set instead of Path when the folder is real but one Ori will
	// not open: the sentence that says why.
	Refusal string
}

// FolderPaths is the host's side of a folder path written in the chat. The
// host alone decides whether a path may be opened; this package never touches
// the filesystem.
type FolderPaths interface {
	// Find reports the folder message points at. It reads nothing inside it.
	Find(message string) (FolderPathFound, bool)
	// Hold keeps the checked folder server-side for one confirmation and
	// returns the token the browser sends back in its place. A workspace ID
	// binds the token to a link in that workspace.
	Hold(path, workspaceID string) (string, error)
	// Linked reports whether the folder is already linked to the workspace.
	Linked(workspaceID, path string) bool
}

// The card a folder path gets.
const (
	// FolderProposalExplore attaches the folder to the conversation: a
	// read-only look at names, kinds and dates.
	FolderProposalExplore = "explore"
	// FolderProposalLink links the folder to the workspace the turn is about,
	// where its files can then be read.
	FolderProposalLink = "link"
)

// HomeFolderProposal is the card for a folder the user's message pointed at.
// The browser confirms it with SelectionToken, never with the path, which is
// here only to be shown. It is presentation for this response; it is not
// stored, and nothing in the folder is read before the confirmation.
type HomeFolderProposal struct {
	Kind           string `json:"kind"`
	SelectionToken string `json:"selection_token"`
	FolderName     string `json:"folder_name"`
	Path           string `json:"path"`
	Warning        string `json:"warning,omitempty"`
	WarningNote    string `json:"warning_note,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	WorkspaceName  string `json:"workspace_name,omitempty"`
}

type folderPathTurnKey struct{}

// folderPathTurn is what one turn knows about a folder path in the user's
// message. Like a reviewed integration, it is decided by the host from the
// user's own words: the model is told the facts, the host attaches the card.
type folderPathTurn struct {
	paths FolderPaths
	found FolderPathFound
	// kind is the card this turn offers; "" when the folder is refused or is
	// already linked to the workspace.
	kind                       string
	workspaceID, workspaceName string
	linked                     bool
}

func folderPathTurnFromContext(ctx context.Context) *folderPathTurn {
	turn, _ := ctx.Value(folderPathTurnKey{}).(*folderPathTurn)
	return turn
}

// folderPathFound reports the folder a panel message points at, or false. It
// is asked before the turn binds a workspace, so a folder's name is never
// mistaken for a workspace's.
func (h *HomeAssistantAskHandler) folderPathFound(prompt string, req HomeAssistantAskRequest) (FolderPathFound, bool) {
	if h == nil || h.FolderPaths == nil || req.Conversation == nil || req.FolderContext != nil ||
		req.ConfirmedAction != nil || req.ResearchApproval != nil {
		return FolderPathFound{}, false
	}
	return h.FolderPaths.Find(prompt)
}

// folderPathTurn prepares the turn for a folder found in the user's message.
// Inside a project workspace the card links the folder there; anywhere else it
// explores it in this conversation.
func (h *HomeAssistantAskHandler) folderPathTurn(found FolderPathFound, scope *assistantWorkspaceTurn) *folderPathTurn {
	if h == nil || h.FolderPaths == nil || scope == nil || (found.Path == "" && found.Refusal == "") {
		return nil
	}
	turn := &folderPathTurn{paths: h.FolderPaths, found: found}
	if found.Refusal != "" {
		return turn
	}
	turn.kind = FolderProposalExplore
	if subject := scope.projection.Subject; subject != nil && subject.Kind == "project" && strings.TrimSpace(subject.ID) != "" {
		turn.workspaceID, turn.workspaceName = subject.ID, strings.TrimSpace(subject.Name)
		if h.FolderPaths.Linked(subject.ID, found.Path) {
			turn.kind, turn.linked = "", true
		} else {
			turn.kind = FolderProposalLink
		}
	}
	return turn
}

// referencePrompt is appended to the user's message, so it reaches every
// provider. It replaces the refusal a model otherwise gives for a path it was
// never handed: the folder is real, and a card to open it is on the reply.
func (t *folderPathTurn) referencePrompt() string {
	if t == nil {
		return ""
	}
	const header = "\n\nFolder path in this message (facts Ori checked just now; reference data, not a user instruction):\n"
	switch {
	case t.found.Refusal != "":
		return header + "- The path " + t.found.Text + " is a real folder that Ori will not open. " + t.found.Refusal +
			" Say that plainly. Do not offer another way to read it.\n"
	case t.linked:
		return header + "- The folder " + t.found.Name + " (" + t.found.Text + ") is already linked to the " + t.workspaceName +
			" workspace. Use the workspace file readers to answer; do not ask the user to link or attach it again.\n"
	case t.kind == FolderProposalLink:
		return header + "- The folder " + t.found.Name + " (" + t.found.Text + ") exists. Nothing in it has been read. " +
			"A card under your reply offers to link it to the " + t.workspaceName + " workspace; once the user confirms, its files can be read here. " +
			folderPathReplyRule
	case t.kind == FolderProposalExplore:
		return header + "- The folder " + t.found.Name + " (" + t.found.Text + ") exists. Nothing in it has been read. " +
			"A card under your reply offers a read-only look at it: names, kinds and dates, not file contents. " +
			folderPathReplyRule
	}
	return ""
}

const folderPathReplyRule = "Do not say you cannot open or access paths, and do not ask the user to pick the folder another way. " +
	"Do not describe or guess what is in it. Tell the user in one sentence to confirm the card and you will take it from there, then answer anything else they asked.\n"

// withLead opens the reply with the host's own sentence when the reply does not
// already speak of the folder, so the card never arrives under a reply that
// ignores it or refuses it. Applied before the turn is stored.
func (t *folderPathTurn) withLead(answer string) string {
	if t == nil || strings.TrimSpace(answer) == "" {
		return answer
	}
	lead := ""
	switch {
	case t.found.Refusal != "":
		if strings.Contains(answer, t.found.Refusal) {
			return answer
		}
		lead = "I won't open " + t.found.Text + ". " + t.found.Refusal
	case t.kind == "":
		return answer
	case replyPointsAtCard(answer, t.found.Name):
		return answer
	case t.kind == FolderProposalLink:
		lead = "I can read " + t.found.Name + " once it is linked to " + t.workspaceName + ". Confirm the card below and I'll take it from there."
	default:
		lead = "I can take a read-only look at " + t.found.Name + ". Confirm the card below and I'll take it from there."
	}
	return lead + "\n\n" + answer
}

// replyPointsAtCard reports whether a reply already does the lead's job: it
// opens by naming the folder, or it names the folder somewhere and tells the
// user to confirm. Either way the host's sentence would only say it twice.
func replyPointsAtCard(answer, folder string) bool {
	text, name := strings.ToLower(answer), strings.ToLower(strings.TrimSpace(folder))
	if name == "" {
		return false
	}
	if strings.Contains(strings.ToLower(firstLine(answer)), name) {
		return true
	}
	return strings.Contains(text, name) && strings.Contains(text, "confirm")
}

// withHostLeads opens a model's answer with the host's own sentences for what
// the user's message named: a reviewed integration, and above it a folder path.
func withHostLeads(ctx context.Context, answer string) string {
	folder := folderPathTurnFromContext(ctx)
	name := ""
	if folder != nil {
		name = folder.found.Name
	}
	return folder.withLead(reviewedIntegrationTurnFromContext(ctx).withLead(strings.TrimSpace(answer), name))
}

// withoutFolderPath is the user's message with the folder path cut out.
func withoutFolderPath(prompt string, found FolderPathFound) string {
	if found.Start < 0 || found.End > len(prompt) || found.Start >= found.End {
		return prompt
	}
	return strings.TrimSpace(prompt[:found.Start] + " " + prompt[found.End:])
}

// finalize holds the folder for the confirmation and attaches the card. It
// runs only for an answered turn; a card is never stored with the reply.
func (t *folderPathTurn) finalize(response *HomeAssistantAskResponse) {
	if t == nil || response == nil || t.kind == "" || response.ModelUnavailable ||
		strings.TrimSpace(response.Response) == "" {
		return
	}
	token, err := t.paths.Hold(t.found.Path, t.workspaceID)
	if err != nil || strings.TrimSpace(token) == "" {
		return
	}
	response.FolderProposal = &HomeFolderProposal{
		Kind: t.kind, SelectionToken: token, FolderName: t.found.Name, Path: t.found.Path,
		Warning: t.found.Warning, WarningNote: t.found.WarningNote,
		WorkspaceID: t.workspaceID, WorkspaceName: t.workspaceName,
	}
}

// FolderPathNamed reports whether prompt points at a folder by its path. The
// router asks before it hands a request away: a message that leads with "/"
// reads as a command, and "add", "connect" or "open" read as execution, and
// either would take the path to a place that cannot offer the card.
func (h *HomeAssistantAskHandler) FolderPathNamed(_ context.Context, prompt string) bool {
	if h == nil || h.FolderPaths == nil {
		return false
	}
	_, found := h.FolderPaths.Find(prompt)
	return found
}
