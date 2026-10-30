package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
)

// Goal-run output contract (workspace-goal-suggestions FR14-FR18, FR21).
//
// A goal run answers with {"items": [...]}; each item is a "task" (work an
// agent in the workspace could do, written to the Backlog as a suggested
// ticket) or a "finding" (something the user should know, stored as an Action
// Center opportunity). Storing them is an application-side effect of parsing
// the answer, not a model tool call, so it happens under every autonomy level.

// Mission item kinds.
const (
	MissionItemTask    = "task"
	MissionItemFinding = "finding"
)

// Per-run caps and run-record bounds.
const (
	// MissionMaxTaskSuggestions caps the suggested tasks one run may store.
	MissionMaxTaskSuggestions = 5
	// MissionMaxFindings caps the findings one run may store.
	MissionMaxFindings = 5
	// HireFirstLookMaxSuggestions caps a hired agent's first look: a few
	// first tasks, and no findings.
	HireFirstLookMaxSuggestions = 3
	// missionSummaryMax and missionNextMax bound a run's summary, its next
	// task and the reason it comes first.
	missionSummaryMax = 600
	missionNextMax    = 200
	// MissionAssessmentMax bounds a run's assessment of the workspace against
	// its goal; ten kept runs hold at most 15,000 runes of it.
	MissionAssessmentMax = 1500
	// MissionRunHistoryLimit is how many run records a workspace keeps.
	MissionRunHistoryLimit = 10
	// MissionOutputTailLimit bounds the raw output kept for a failed run.
	MissionOutputTailLimit = 2000
)

// TicketSourceGoalSuggestion marks a Backlog ticket a goal run suggested. It
// rides on Task.SourceType, which BACKLOG.md sync never rewrites (the file
// carries only id/title/priority/tags/url), so the marker survives a round trip.
const TicketSourceGoalSuggestion = "goal_suggestion"

// MissionSuggestionContextKey holds a suggested ticket's structured reason on
// Task.Context, so the Backlog drawer can show the why and evidence without
// parsing the Markdown details.
const MissionSuggestionContextKey = "goal_suggestion"

// Parse statuses recorded on a run.
const (
	MissionParseOK      = "ok"
	MissionParsePartial = "partial"
	MissionParseFailed  = "failed"
)

// ErrMissionOutputEmpty reports a run that answered nothing at all.
var ErrMissionOutputEmpty = errors.New("the goal run returned no output")

// MissionTaskSuggestion is one parsed "task" item.
type MissionTaskSuggestion struct {
	Title          string
	Why            string
	Evidence       string
	Priority       string
	Confidence     string
	Effort         string
	SuggestedAgent string
	FirstStep      string
}

// MissionRunOutput is a parsed goal-run answer. Rejected counts items that
// were present but unusable (unknown kind, no title, an invalid enum value).
// Summary, Next and NextWhy are what a hired agent's first look says about
// itself; other runs leave them empty. Assessment is where the run found the
// workspace standing against its goal; an older-shaped answer leaves it empty.
type MissionRunOutput struct {
	Tasks      []MissionTaskSuggestion
	Findings   []Opportunity
	Rejected   int
	Summary    string
	Next       string
	NextWhy    string
	Assessment string
}

// MissionSuggestionLimits is how many tasks and findings a run of this
// origin may store.
func MissionSuggestionLimits(origin string) (tasks, findings int) {
	if origin == MissionOriginHireFirstLook {
		return HireFirstLookMaxSuggestions, 0
	}
	return MissionMaxTaskSuggestions, MissionMaxFindings
}

// MissionRunSummary is the summary a run record keeps: the answer's summary,
// ending in a "Next:" line that names the task to start first — the one the
// answer named, else the first suggestion stored. Empty when the answer gave
// neither. softwareassessment.NextTask reads the line back.
func MissionRunSummary(output MissionRunOutput, stored []MissionTaskSuggestion) string {
	next := output.Next
	if next == "" && len(stored) > 0 {
		next = stored[0].Title
	}
	lines := []string{}
	if output.Summary != "" {
		lines = append(lines, output.Summary)
	}
	if next != "" {
		lines = append(lines, "Next: "+next)
	}
	return strings.Join(lines, "\n")
}

// missionOneLine flattens model text to one bounded line.
func missionOneLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if runes := []rune(value); len(runes) > max {
		value = strings.TrimSpace(string(runes[:max]))
	}
	return value
}

// missionParagraphs keeps model text's paragraph breaks — trailing spaces
// trimmed, runs of blank lines collapsed to one — bounded on a rune boundary.
func missionParagraphs(value string, max int) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			blank = true
			continue
		}
		if blank && len(kept) > 0 {
			kept = append(kept, "")
		}
		blank = false
		kept = append(kept, line)
	}
	value = strings.Join(kept, "\n")
	if runes := []rune(value); len(runes) > max {
		value = strings.TrimSpace(string(runes[:max]))
	}
	return value
}

// missionAssessmentText reads the assessment field: a string, or — from a
// model that structured it anyway — an object of part names to text, which
// becomes "Part: text" paragraphs in canonical order. Anything else, or a
// missing field, is no assessment; it never fails the run.
func missionAssessmentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return missionParagraphs(text, MissionAssessmentMax)
	}
	var parts map[string]string
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var paragraphs []string
	used := map[string]bool{}
	for _, part := range GoalCharterParts {
		for key, body := range parts {
			if strings.EqualFold(strings.TrimSpace(key), part.Name) && strings.TrimSpace(body) != "" {
				paragraphs = append(paragraphs, part.Name+": "+strings.TrimSpace(body))
				used[key] = true
			}
		}
	}
	others := make([]string, 0, len(parts))
	for key := range parts {
		if !used[key] && strings.TrimSpace(parts[key]) != "" {
			others = append(others, key)
		}
	}
	sort.Strings(others)
	for _, key := range others {
		paragraphs = append(paragraphs, strings.TrimSpace(key)+": "+strings.TrimSpace(parts[key]))
	}
	return missionParagraphs(strings.Join(paragraphs, "\n\n"), MissionAssessmentMax)
}

// ParseStatus summarizes how cleanly the answer parsed.
func (o MissionRunOutput) ParseStatus() string {
	if o.Rejected > 0 {
		return MissionParsePartial
	}
	return MissionParseOK
}

// MissionRunRecord is one goal run in the workspace's run history (FR21).
type MissionRunRecord struct {
	RunID             string    `json:"run_id,omitempty"`
	Origin            string    `json:"origin,omitempty"` // cadence | manual | first_goal | event
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
	Succeeded         bool      `json:"succeeded"`
	ParseStatus       string    `json:"parse_status,omitempty"`
	TasksCreated      int       `json:"tasks_created"`
	FindingsCreated   int       `json:"findings_created"`
	DuplicatesSkipped int       `json:"duplicates_skipped"`
	DroppedByCap      int       `json:"dropped_by_cap"`
	ItemsRejected     int       `json:"items_rejected,omitempty"`
	Error             string    `json:"error,omitempty"`
	// OutputTail is the end of the raw answer, kept only when it could not be
	// read, so the user can see what the model actually said.
	OutputTail string `json:"output_tail,omitempty"`
	// Summary is what a hired agent's first look found, ending in a "Next:"
	// line (MissionRunSummary); NextWhy is its one sentence on why that task
	// comes first. Other runs leave both empty.
	Summary string `json:"summary,omitempty"`
	NextWhy string `json:"next_why,omitempty"`
	// Assessment is where the run found the workspace standing against its
	// goal (paragraphs, bounded at MissionAssessmentMax), for every origin.
	// AssessmentParts names the charter parts the goal had when the run
	// started, so the card can tell an assessment of an older goal.
	Assessment      string   `json:"assessment,omitempty"`
	AssessmentParts []string `json:"assessment_parts,omitempty"`
}

// RecordMissionRun prepends a run record and keeps the newest
// MissionRunHistoryLimit. Mutates ws; callers hold it inside Store.Update.
func RecordMissionRun(ws *Workspace, record MissionRunRecord) {
	if ws == nil {
		return
	}
	runs := append([]MissionRunRecord{record}, ws.MissionRuns...)
	if len(runs) > MissionRunHistoryLimit {
		runs = runs[:MissionRunHistoryLimit]
	}
	ws.MissionRuns = runs
}

// MissionOutputTail returns the last MissionOutputTailLimit bytes of raw,
// cut on a rune boundary.
func MissionOutputTail(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= MissionOutputTailLimit {
		return raw
	}
	tail := raw[len(raw)-MissionOutputTailLimit:]
	for len(tail) > 0 && !isRuneStart(tail[0]) {
		tail = tail[1:]
	}
	return "…" + tail
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

var missionFencePattern = regexp.MustCompile("(?s)```[a-zA-Z0-9_-]*[ \\t]*\\n(.*?)\\n?```")

// missionJSONPayload finds the JSON in an answer: the whole answer when it is
// JSON, otherwise the single fenced block it contains.
func missionJSONPayload(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return trimmed, nil
	}
	blocks := missionFencePattern.FindAllStringSubmatch(trimmed, -1)
	switch len(blocks) {
	case 0:
		return "", errors.New("the answer is not JSON and contains no fenced JSON block")
	case 1:
		return strings.TrimSpace(blocks[0][1]), nil
	default:
		return "", fmt.Errorf("the answer contains %d fenced blocks; expected one JSON object", len(blocks))
	}
}

type missionItem struct {
	Kind              string `json:"kind"`
	Title             string `json:"title"`
	Why               string `json:"why"`
	Summary           string `json:"summary"` // legacy findings carried the reason here
	Evidence          string `json:"evidence"`
	Priority          string `json:"priority"`
	Confidence        string `json:"confidence"`
	Effort            string `json:"effort"`
	SuggestedAgent    string `json:"suggested_agent"`
	FirstStep         string `json:"first_step"`
	RecommendedAction string `json:"recommended_action"`
}

// ParseMissionRunOutput reads a goal run's answer. It accepts bare JSON or one
// fenced block, the {"items": [...]} contract, and — for models that still
// answer in the previous shape — {"findings": [...]} or a bare array, whose
// entries are findings unless they say otherwise. An error means the answer
// could not be read at all; individual unusable items are counted in Rejected.
func ParseMissionRunOutput(workspaceID, sourceRunID, raw string) (MissionRunOutput, error) {
	var out MissionRunOutput
	if strings.TrimSpace(raw) == "" {
		return out, ErrMissionOutputEmpty
	}
	payload, err := missionJSONPayload(raw)
	if err != nil {
		return out, err
	}

	var items []missionItem
	legacyKind := ""
	if strings.HasPrefix(payload, "[") {
		if err := json.Unmarshal([]byte(payload), &items); err != nil {
			return out, fmt.Errorf("goal run output: %w", err)
		}
		legacyKind = MissionItemFinding
	} else {
		var envelope struct {
			Items      *[]missionItem  `json:"items"`
			Findings   *[]missionItem  `json:"findings"`
			Summary    string          `json:"summary"`
			Next       string          `json:"next"`
			NextWhy    string          `json:"next_why"`
			Assessment json.RawMessage `json:"assessment"`
		}
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			return out, fmt.Errorf("goal run output: %w", err)
		}
		out.Summary = missionOneLine(envelope.Summary, missionSummaryMax)
		out.Next = missionOneLine(envelope.Next, missionNextMax)
		out.NextWhy = missionOneLine(envelope.NextWhy, missionSummaryMax)
		out.Assessment = missionAssessmentText(envelope.Assessment)
		switch {
		case envelope.Items != nil:
			items = *envelope.Items
		case envelope.Findings != nil:
			items, legacyKind = *envelope.Findings, MissionItemFinding
		default:
			return out, errors.New(`goal run output has no "items" list`)
		}
	}

	now := time.Now()
	for _, item := range items {
		kind := strings.ToLower(strings.TrimSpace(item.Kind))
		if kind == "" {
			kind = legacyKind
		}
		title := strings.Join(strings.Fields(item.Title), " ")
		priority, priorityOK := normalizeMissionEnum(item.Priority, "medium", "low", "medium", "high", "critical")
		confidence, confidenceOK := normalizeMissionEnum(item.Confidence, "medium", "low", "medium", "high")
		if title == "" || !priorityOK || !confidenceOK {
			out.Rejected++
			continue
		}
		why := strings.TrimSpace(item.Why)
		if why == "" {
			why = strings.TrimSpace(item.Summary)
		}
		switch kind {
		case MissionItemTask:
			effort, ok := normalizeMissionEnum(item.Effort, "", "small", "medium", "large")
			if !ok {
				out.Rejected++
				continue
			}
			out.Tasks = append(out.Tasks, MissionTaskSuggestion{
				Title: title, Why: why, Evidence: strings.TrimSpace(item.Evidence),
				Priority: priority, Confidence: confidence, Effort: effort,
				SuggestedAgent: strings.TrimSpace(item.SuggestedAgent), FirstStep: strings.TrimSpace(item.FirstStep),
			})
		case MissionItemFinding:
			out.Findings = append(out.Findings, Opportunity{
				WorkspaceID:       workspaceID,
				SourceRunID:       sourceRunID,
				Title:             title,
				Summary:           why,
				Evidence:          strings.TrimSpace(item.Evidence),
				Priority:          priority,
				Confidence:        confidence,
				Status:            OpportunityNew,
				RecommendedAction: strings.TrimSpace(item.RecommendedAction),
				CreatedAt:         now,
				UpdatedAt:         now,
			})
		default:
			out.Rejected++
		}
	}
	return out, nil
}

// normalizeMissionEnum lowercases value and checks it against allowed. Empty
// takes the fallback; anything else outside allowed is invalid.
func normalizeMissionEnum(value, fallback string, allowed ...string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return fallback, true
	}
	for _, candidate := range allowed {
		if normalized == candidate {
			return normalized, true
		}
	}
	return "", false
}

// NormalizeSuggestionTitle is the dedupe key for suggested work: case,
// whitespace and trailing punctuation do not make a title new (FR18).
func NormalizeSuggestionTitle(title string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(title), " "))
	return strings.TrimSpace(strings.TrimRight(normalized, ".!?;:,…-–— "))
}

// OpenWorkTitles returns the normalized titles of every ticket in the
// workspace that is not finished: Backlog, Ready and active work alike. A
// suggestion matching one of them is a duplicate.
func OpenWorkTitles(ws *Workspace) map[string]struct{} {
	titles := map[string]struct{}{}
	if ws == nil {
		return titles
	}
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	for _, task := range ws.Tasks {
		switch task.CanonicalState() {
		case TicketStateDone, TicketStateCancelled:
			continue
		}
		if key := NormalizeSuggestionTitle(task.Description); key != "" {
			titles[key] = struct{}{}
		}
	}
	return titles
}

// SelectMissionSuggestions dedupes suggestions against known titles and each
// other, then applies the per-run cap. known is not modified.
func SelectMissionSuggestions(suggestions []MissionTaskSuggestion, known map[string]struct{}, limit int) (kept []MissionTaskSuggestion, duplicates, dropped int) {
	seen := make(map[string]struct{}, len(known)+len(suggestions))
	for key := range known {
		seen[key] = struct{}{}
	}
	for _, suggestion := range suggestions {
		key := NormalizeSuggestionTitle(suggestion.Title)
		if _, dup := seen[key]; dup {
			duplicates++
			continue
		}
		seen[key] = struct{}{}
		if limit > 0 && len(kept) >= limit {
			dropped++
			continue
		}
		kept = append(kept, suggestion)
	}
	return kept, duplicates, dropped
}

// MissionSuggestionPriority maps a suggestion priority onto the 1 (highest)
// to 5 ticket scale.
func MissionSuggestionPriority(priority string) int {
	switch priority {
	case "critical":
		return 1
	case "high":
		return 2
	case "low":
		return 4
	default:
		return TicketPriorityDefault
	}
}

// MissionSuggestionDetails renders a suggestion's reason as the ticket's
// Markdown description, which is what a person (and BACKLOG.md readers) see.
func MissionSuggestionDetails(s MissionTaskSuggestion, runID string, at time.Time) string {
	var b strings.Builder
	write := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(&b, "**%s:** %s\n\n", label, value)
		}
	}
	write("Why", s.Why)
	write("Evidence", s.Evidence)
	write("First step", s.FirstStep)
	var facts []string
	if s.SuggestedAgent != "" {
		facts = append(facts, "**Suggested agent:** "+s.SuggestedAgent)
	}
	if s.Effort != "" {
		facts = append(facts, "**Effort:** "+s.Effort)
	}
	if s.Confidence != "" {
		facts = append(facts, "**Confidence:** "+s.Confidence)
	}
	if len(facts) > 0 {
		b.WriteString(strings.Join(facts, " · "))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "_Suggested by a goal run on %s (run %s)._", at.UTC().Format("2006-01-02"), runID)
	return b.String()
}

// MissionSuggestionContext is the structured reason stored on a suggested
// ticket under MissionSuggestionContextKey.
func MissionSuggestionContext(s MissionTaskSuggestion, runID string) map[string]any {
	return map[string]any{
		"why":             s.Why,
		"evidence":        s.Evidence,
		"first_step":      s.FirstStep,
		"suggested_agent": s.SuggestedAgent,
		"effort":          s.Effort,
		"confidence":      s.Confidence,
		"run_id":          runID,
	}
}

// Precondition kinds, ticket actions and who wrote a precondition's ticket
// (backup-first FR10, FR11). The shelf maps an action id to its button.
const (
	MissionPreconditionBackup    = "backup_protection"
	MissionActionCheckProtection = "check_protection"
	MissionSuggestionSourceModel = "model"
	MissionSuggestionSourceHost  = "host"
)

// backupPreconditionFirstStep is the backup task's first step (PRD §6.2), and
// backupPreconditionWhy its reason. Neither claims a backup exists or not.
const (
	backupPreconditionFirstStep = "Press Back up and approve one metadata read."
	backupPreconditionWhy       = "Filing moves the originals, so a recovery copy of this folder should exist first."
)

// BackupPrecondition is a File Janitor folder's backup precondition: unmet
// while its stored protection fact is not_covered, unknown or configured (a
// saved configuration is not a completed backup). Its statement is the fact's
// own prompt wording. false for covered, unavailable or an unnamed folder.
func BackupPrecondition(folderName string, fact filecuratorguidance.ProtectionFact) (MissionPrecondition, bool) {
	folder := missionPromptLine(folderName)
	switch fact.State {
	case "not_covered", "unknown", "configured":
	default:
		return MissionPrecondition{}, false
	}
	// Prompt() opens with its own heading; the section supplies one.
	statement := strings.TrimSpace(fact.Prompt())
	if _, body, headed := strings.Cut(statement, "\n"); headed && strings.HasPrefix(statement, "## ") {
		statement = strings.TrimSpace(body)
	}
	if folder == "" || statement == "" {
		return MissionPrecondition{}, false
	}
	return MissionPrecondition{
		Kind:               MissionPreconditionBackup,
		Statement:          statement,
		SuggestedTitle:     "Back up " + folder + " before the first filing batch",
		SuggestedFirstStep: backupPreconditionFirstStep,
		Why:                backupPreconditionWhy,
		Action:             MissionActionCheckProtection,
	}, true
}

// HostPreconditionSuggestion is the ticket the host stores for an unmet
// precondition the run's answer left out (backup-first FR11): its own title,
// reason and first step, the fact as evidence, high priority.
func HostPreconditionSuggestion(p MissionPrecondition) MissionTaskSuggestion {
	return MissionTaskSuggestion{
		Title: p.SuggestedTitle, Why: p.Why, Evidence: p.Statement,
		Priority: "high", Confidence: "high", Effort: "small", FirstStep: p.SuggestedFirstStep,
	}
}

// MatchMissionPrecondition is the precondition a suggestion's title meets,
// compared after NormalizeSuggestionTitle.
func MatchMissionPrecondition(title string, preconditions []MissionPrecondition) (MissionPrecondition, bool) {
	key := NormalizeSuggestionTitle(title)
	for _, p := range preconditions {
		if key != "" && NormalizeSuggestionTitle(p.SuggestedTitle) == key {
			return p, true
		}
	}
	return MissionPrecondition{}, false
}

// TagMissionPrecondition marks a suggested ticket's context as meeting a
// precondition: which one, the button it offers, and who wrote it.
func TagMissionPrecondition(context map[string]any, p MissionPrecondition, source string) map[string]any {
	if context == nil {
		context = map[string]any{}
	}
	context["precondition"] = p.Kind
	context["action"] = p.Action
	context["source"] = source
	return context
}
