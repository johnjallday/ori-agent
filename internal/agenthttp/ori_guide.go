package agenthttp

import (
	"net/http"
	"net/url"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Ori Guide: the app's setup-and-navigation helper.
//
// This handler is a deliberately separate contract from home_assistant_ask.go,
// which routes and executes real work. The separation is structural rather than
// procedural: GuideAction below has no field capable of expressing "create",
// "delete", "send", "grant", or "run". There is no action string, no arguments
// map, and no confirmed-action path, so a mutation cannot be represented here
// even by a caller trying to construct one (PRD FR-29/FR-36/FR-37/FR-38).
//
// The handler also has no mutator dependency: it holds a read-only workspace
// store for resolving names, and nothing else. It cannot invoke an agent, start
// a task, call a connected service, or read a secret, because it has no
// reference to anything that could (FR-39).
//
// Ori may offer a bounded set of *setup* steps — see SetupStep — but even those
// resolve to "open this validated route"; the guide never performs them.

// GuideActionType is a closed union. Adding a member is a reviewed change to
// what Ori is permitted to do, not a configuration tweak.
type GuideActionType string

const (
	// GuideActionNavigate opens a destination validated against the navigation
	// catalog. Rendered as an ordinary link so existing route guards, unsaved-edit
	// warnings, and server permissions all still apply (FR-36/FR-49).
	GuideActionNavigate GuideActionType = "navigate"
	// GuideActionCoachmark visually marks a control by typed key. It never
	// activates the control (FR-42).
	GuideActionCoachmark GuideActionType = "coachmark"
	// GuideActionHandoff offers an explicit transition to the Personal Assistant.
	// It is never a submission or approval to execute work.
	GuideActionHandoff GuideActionType = "handoff"
	// GuideActionSetup opens one of the enumerated setup surfaces.
	GuideActionSetup GuideActionType = "setup"
	// GuideActionReset clears the guide back to its suggested topics.
	GuideActionReset GuideActionType = "reset"
	// GuideActionDismiss closes the guide.
	GuideActionDismiss GuideActionType = "dismiss"
)

// GuideAction is what the guide may hand the browser.
//
// Every field is a typed, server-validated value. Href is only ever copied from
// the navigation catalog, Coachmark is only ever a registered key, and SetupStep
// is only ever an enumerated constant — none of them are free text the model or
// the client can fill in.
type GuideAction struct {
	Type      GuideActionType `json:"type"`
	Label     string          `json:"label"`
	Href      string          `json:"href,omitempty"`
	NavKey    string          `json:"nav_key,omitempty"`
	Coachmark CoachmarkKey    `json:"coachmark,omitempty"`
	SetupStep SetupStep       `json:"setup_step,omitempty"`
	// HandoffText is the user's own words, echoed back for the work surface to
	// prefill. It is never executed here and never auto-submitted there.
	HandoffText string `json:"handoff_text,omitempty"`
}

// GuideRequest is the bounded input the guide accepts. There is no room for
// page contents: a route, a short question, and nothing else (FR-35).
type GuideRequest struct {
	Question string `json:"question"`
	Route    string `json:"route,omitempty"`
}

// maxGuideQuestion bounds the request so the guide cannot be used as a channel
// for shipping page text to a model (FR-35, Technical Consideration 7.4).
const maxGuideQuestion = 400

// GuideTopicSummary is an approved topic offered as a suggestion.
type GuideTopicSummary struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// GuideResponse is the guide's reply.
type GuideResponse struct {
	// Status distinguishes a real answer from an honest miss, so the UI never
	// renders "I don't know" as though it were an explanation (FR-48/FR-124).
	Status string `json:"status"` // answered | unknown
	// TopicKey is the approved topic this answer came from, when there is one.
	TopicKey  string              `json:"topic_key,omitempty"`
	Location  string              `json:"location,omitempty"`
	About     string              `json:"about"`
	Answer    string              `json:"answer"`
	Actions   []GuideAction       `json:"actions,omitempty"`
	Suggested []GuideTopicSummary `json:"suggested,omitempty"`
}

// GuideHandler serves Ori Guide.
type GuideHandler struct {
	// workspaceStore is read-only here: it resolves a workspace name the user
	// mentioned to a real id so "open my Launch workspace" can be offered. It is
	// never used to mutate.
	workspaceStore workspace.Store
	// phraser optionally restates an already-approved answer more naturally. It
	// is text-in/text-out and runs after every decision has been made, so it can
	// change how an answer reads but never what it says to do (FR-46).
	phraser GuidePhraser
}

// NewGuideHandler builds the guide. It takes no agent store, no LLM factory, no
// task executor, and no vault — the absence of those dependencies is what makes
// the safety boundary structural rather than a matter of discipline.
func NewGuideHandler() *GuideHandler {
	return &GuideHandler{}
}

// SetWorkspaceStore attaches the read-only workspace store once it exists.
//
// Handlers are wired a phase before the workspace store is built, so capturing
// it at construction time would silently bind nil. Everything the guide does
// today works without it; it is used only to resolve a workspace the user named
// to a real id before offering to open it.
func (h *GuideHandler) SetWorkspaceStore(ws workspace.Store) {
	if h == nil {
		return
	}
	h.workspaceStore = ws
}

// ServeHTTP handles POST /api/ori-guide.
func (h *GuideHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req GuideRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	question := strings.TrimSpace(req.Question)
	if runes := []rune(question); len(runes) > maxGuideQuestion {
		question = string(runes[:maxGuideQuestion])
	}
	route := sanitizeGuideRoute(req.Route)

	resp := h.answer(question, route)
	resp.About = aboutThisScreen(route)

	// Phrasing runs last, on the finished response, and can only replace answer
	// prose. Topic, actions, location, and suggestions are already decided and
	// are not passed to the model (FR-46).
	if resp.Status == "answered" {
		resp.Answer = h.phraseAnswer(r.Context(), question, resp.Answer)
	}

	orihttp.WriteJSON(w, resp)
}

// sanitizeGuideRoute keeps the route a bounded, internal path. Anything that
// looks like an absolute URL, a scheme, or a traversal is discarded rather than
// echoed back — route context is a hint for ordering suggestions, not something
// worth trusting.
func sanitizeGuideRoute(raw string) string {
	route := strings.TrimSpace(raw)
	if route == "" || !strings.HasPrefix(route, "/") {
		return "/"
	}
	if len(route) > 200 || strings.HasPrefix(route, "//") || strings.Contains(route, "://") {
		return "/"
	}
	route = strings.SplitN(strings.SplitN(route, "?", 2)[0], "#", 2)[0]
	decoded, err := url.PathUnescape(route)
	if err != nil || strings.Contains(decoded, "..") || strings.Contains(decoded, "\\") || strings.HasPrefix(decoded, "//") {
		return "/"
	}
	for _, r := range decoded {
		if r <= 32 || r == 127 {
			return "/"
		}
	}
	return route
}

// answer is the deterministic core. It runs without a model and produces the
// same result every time, which is what keeps the guide usable when no model is
// configured, when the provider is down, and when a request times out
// (FR-47/FR-8).
func (h *GuideHandler) answer(question, route string) GuideResponse {
	resp := GuideResponse{
		Status:    "unknown",
		Location:  locationLabelFor(route),
		Suggested: summarizeGuideTopics(suggestedTopicsFor(route)),
	}

	if question == "" {
		resp.Answer = "Search reviewed help topics to understand the app. This is a bounded topic search, not full-text documentation search."
		return resp
	}

	// A work request is recognized before topic matching, so "send the summary
	// to marketing" gets an honest handoff rather than being bent into whichever
	// topic shares a word with it (FR-40).
	if isWorkRequest(question) || isGuideWorkCommand(question) {
		resp.Status = "answered"
		resp.TopicKey = "workspace-manager"
		// Preserve the stable topic key for compatibility, not execution dispatch.
		resp.Answer = "This looks like work for your Personal Assistant. Help only explains the app. " +
			"You can open your assistant to review the request; nothing has been sent."
		resp.Actions = []GuideAction{{
			Type:        GuideActionHandoff,
			Label:       "Open your assistant",
			HandoffText: question,
		}}
		return resp
	}

	// A named real record beats a generic topic: someone asking about "the
	// Launch workspace" wants that workspace, not the definition of the word
	// (FR-32). Checked after the work-request test so "delete the Launch
	// workspace" is still a handoff rather than an invitation to open it.
	if dyn, ok := h.dynamicWorkspaceResponse(question, route); ok {
		return dyn
	}

	topic, ok := FindGuideTopic(question)
	if !ok {
		// An honest miss. The approved topics are offered instead of a guess.
		resp.Answer = "No matching help topic. Try one of the common questions below or search for an app term."
		return resp
	}

	resp.Status = "answered"
	resp.TopicKey = topic.Key
	resp.Answer = topic.Explanation
	resp.Actions = h.actionsFor(topic, route)
	return resp
}

// actionsFor builds the typed actions for a topic. Every destination is looked
// up in the navigation catalog rather than composed here, so an action can only
// ever point at a registered route (FR-31/FR-49).
func (h *GuideHandler) actionsFor(topic GuideTopic, route string) []GuideAction {
	var actions []GuideAction

	if topic.Setup != "" {
		if navKey, ok := setupStepRoutes[topic.Setup]; ok {
			if entry, found := FindHomeNavEntry(navKey); found {
				actions = append(actions, GuideAction{
					Type:      GuideActionSetup,
					Label:     "Open " + entry.Label,
					Href:      entry.Href,
					NavKey:    entry.Key,
					SetupStep: topic.Setup,
				})
			}
		}
	}

	if topic.NavKey != "" {
		if entry, found := FindHomeNavEntry(topic.NavKey); found {
			// Suppress a navigate action that would just reload the page the user
			// is already looking at; the coachmark below is the useful part there.
			if !sameRoute(entry.Href, route) && !hasNavAction(actions, entry.Key) {
				actions = append(actions, GuideAction{
					Type:   GuideActionNavigate,
					Label:  "Open " + entry.Label,
					Href:   entry.Href,
					NavKey: entry.Key,
				})
			}
		}
	}

	// A coachmark is only offered once the user is actually on the route that
	// owns the control. Pointing at a control that is not on screen would be a
	// promise the browser cannot keep (FR-43).
	if topic.Coachmark != "" && topic.NavKey != "" {
		if entry, found := FindHomeNavEntry(topic.NavKey); found && sameRoute(entry.Href, route) {
			actions = append(actions, GuideAction{
				Type:      GuideActionCoachmark,
				Label:     "Show me where",
				Coachmark: topic.Coachmark,
			})
		}
	}

	return actions
}

func hasNavAction(actions []GuideAction, navKey string) bool {
	for _, a := range actions {
		if a.NavKey == navKey {
			return true
		}
	}
	return false
}

func sameRoute(href, route string) bool {
	h := strings.TrimSuffix(href, "/")
	r := strings.TrimSuffix(route, "/")
	if h == "" {
		h = "/"
	}
	if r == "" {
		r = "/"
	}
	return strings.EqualFold(h, r)
}

// locationLabelFor names where the user currently is, using the navigation
// catalog's own label so the guide and the app never disagree about what a page
// is called (FR-5/FR-23).
func locationLabelFor(route string) string {
	for _, entry := range HomeNavCatalog() {
		if sameRoute(entry.Href, route) {
			return entry.Label
		}
	}
	// Longest registered prefix, so /workspace/abc reports Workspaces.
	best, bestLen := "", 0
	for _, entry := range HomeNavCatalog() {
		if entry.Href == "/" {
			continue
		}
		if strings.HasPrefix(route, entry.Href+"/") && len(entry.Href) > bestLen {
			best, bestLen = entry.Label, len(entry.Href)
		}
	}
	return best
}

// aboutThisScreen uses only reviewed route/topic metadata, never page contents.
func aboutThisScreen(route string) string {
	if strings.HasPrefix(route, "/workspaces/") {
		parts := strings.Split(strings.TrimPrefix(route, "/workspaces/"), "/")
		if len(parts) > 1 && parts[1] == "canvas" {
			return "The workspace canvas shows agents, tasks, and their connections. Select a node to inspect it; workspace work stays in the workspace or your Personal Assistant."
		}
		if len(parts) > 1 && (parts[1] == "task" || parts[1] == "tasks") {
			return "This workspace task page shows the task, its progress, and results. Review the task here; Help never starts or changes it."
		}
	}
	label := locationLabelFor(route)
	for _, entry := range HomeNavCatalog() {
		if entry.Label == label {
			return entry.Description
		}
	}
	return "A specific overview is not available for this screen. Search the reviewed app topics below to find related guidance."
}

func isGuideWorkCommand(question string) bool {
	q := strings.ToLower(strings.TrimSpace(question))
	for _, command := range []string{"/ask", "/task", "/note"} {
		if q == command || strings.HasPrefix(q, command+" ") || strings.HasPrefix(q, command+"\n") || strings.HasPrefix(q, command+"\t") {
			return true
		}
	}
	return false
}

// workVerbs are the openings that mean "do something", as opposed to "where is"
// or "what is". Kept deliberately blunt: a false positive costs the user one
// extra click on a handoff, while a false negative would have Ori answer a work
// request as though it were a navigation question.
var workVerbs = []string{
	"create ", "make ", "build ", "delete ", "remove ", "send ", "email ", "draft ",
	"schedule ", "run ", "start ", "execute ", "assign ", "install ", "connect ",
	"grant ", "approve ", "write ", "summarize ", "summarise ", "analyze ", "analyse ",
	"fix ", "update ", "rename ", "add ", "set up my", "clean up", "organize ", "organise ",
}

func isWorkRequest(question string) bool {
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return false
	}
	// Questions are navigation/explanation intents even when they contain a work
	// verb: "where do I create an agent" is a find, not a request to create one.
	for _, prefix := range []string{"where", "what", "how do i find", "which page", "who is", "what is"} {
		if strings.HasPrefix(q, prefix) {
			return false
		}
	}
	for _, verb := range workVerbs {
		if strings.HasPrefix(q, verb) {
			return true
		}
	}
	return false
}

func summarizeGuideTopics(topics []GuideTopic) []GuideTopicSummary {
	out := make([]GuideTopicSummary, 0, len(topics))
	for _, t := range topics {
		out = append(out, GuideTopicSummary{Key: t.Key, Label: t.Label})
	}
	return out
}
