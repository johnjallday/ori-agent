package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

const softwareDigestAPI = "/api/personal-assistant/folder-digest"

// softwareCardFixture is the production server with one software fixture in
// its sandboxed Documents folder, shown through the real scan route.
type softwareCardFixture struct {
	*draftServerFixture
	provider *capturingChatProvider
	dir      string
	offer    string
	// set is every fixture; all but the one in Documents are still in staging,
	// where no chip looks.
	set softwarefixture.Set
}

// newSoftwareCardFixture moves one fixture into Documents and scans it. The
// capturing provider is registered but no system model is set, so any model
// call a test did not ask for shows up as a request.
func newSoftwareCardFixture(t *testing.T, fixture string) *softwareCardFixture {
	t.Helper()
	f := &softwareCardFixture{draftServerFixture: newDraftServerFixture(t), provider: &capturingChatProvider{}}
	f.builder.llmFactory.Register("claude_code", f.provider)
	home := os.Getenv("HOME") // sandboxed by the fixture
	// Registered after the fixture's temporary folder, so it runs before that
	// folder is removed.
	t.Cleanup(func() { waitForQuietTree(home) })
	staging := filepath.Join(home, "Staging")
	documents := filepath.Join(home, "Documents")
	for _, dir := range []string{staging, documents} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	set, err := softwarefixture.Build(staging)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { softwarefixture.Restore(set.Unreadable) })
	f.set = set
	f.dir = filepath.Join(documents, fixture)
	if err := os.Rename(filepath.Join(set.Root, fixture), f.dir); err != nil {
		t.Fatal(err)
	}
	// The server holds, links and reports the canonical path.
	if f.dir, err = filepath.EvalSymlinks(f.dir); err != nil {
		t.Fatal(err)
	}
	status, body := f.call(t, http.MethodPost, softwareDigestAPI+"/scan", map[string]any{"chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("scan: %d %v", status, body)
	}
	f.offer = body["offer"].(map[string]any)["id"].(string)
	return f
}

// waitForQuietTree returns once nothing under dir has changed for a moment. A
// finished setup still has listeners writing behind it (quests, the Home's own
// records); the test's temporary folder must not be removed under them.
func waitForQuietTree(dir string) {
	snapshot := func() string {
		var out strings.Builder
		_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if info, statErr := entry.Info(); statErr == nil {
				out.WriteString(path + "|" + info.ModTime().String() + "|" + strconv.FormatInt(info.Size(), 10) + "\n")
			}
			return nil
		})
		return out.String()
	}
	previous, quiet := snapshot(), 0
	for deadline := time.Now().Add(5 * time.Second); quiet < 3 && time.Now().Before(deadline); {
		time.Sleep(60 * time.Millisecond)
		if current := snapshot(); current == previous {
			quiet++
		} else {
			previous, quiet = current, 0
		}
	}
}

func (f *softwareCardFixture) post(t *testing.T, action string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return f.call(t, http.MethodPost, softwareDigestAPI+"/offers/"+f.offer+"/"+action, body)
}

// agentNames is the saved agents' names, sorted. An agent's status and counters
// change on their own; which agents exist does not.
func (f *softwareCardFixture) agentNames(t *testing.T) string {
	t.Helper()
	if f.builder.st == nil {
		t.Fatal("no agent store")
	}
	names := f.builder.st.ListAgents()
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (f *softwareCardFixture) read(t *testing.T) map[string]any {
	t.Helper()
	status, body := f.call(t, http.MethodGet, softwareDigestAPI+"?offer_id="+f.offer, nil)
	if status != http.StatusOK {
		t.Fatalf("read offer: %d %v", status, body)
	}
	return body["folder_digest"].(map[string]any)["offer"].(map[string]any)
}

func (f *softwareCardFixture) review(t *testing.T) map[string]any {
	t.Helper()
	status, body := f.post(t, "review", map[string]any{"request_id": "review-1"})
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, body)
	}
	return body["offer"].(map[string]any)
}

func planLines(offer map[string]any) []map[string]any {
	plan, _ := offer["plan"].(map[string]any)
	if plan == nil {
		return nil
	}
	var lines []map[string]any
	for _, line := range plan["lines"].([]any) {
		lines = append(lines, line.(map[string]any))
	}
	return lines
}

func prerequisite(offer map[string]any, id string) map[string]any {
	plan, _ := offer["plan"].(map[string]any)
	if plan == nil {
		return nil
	}
	rows, _ := plan["prerequisites"].([]any)
	for _, row := range rows {
		if item := row.(map[string]any); item["id"] == id {
			return item
		}
	}
	return nil
}

func strs(value any) string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.(string))
	}
	return strings.Join(out, ",")
}

// The review of a software project, built by the real host: what was read, a
// plan with no integration to install, honest prerequisites, and no side
// effect of any kind.
func TestSoftwareReview_RealPlanAndPrerequisitesWithNoSideEffects(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	workspaces := len(f.builder.workspaceFileStore.CachedWorkspaces())
	agentsBefore := f.agentNames(t)
	before, err := softwarefixture.Hash(f.dir)
	if err != nil {
		t.Fatal(err)
	}

	shown := f.read(t)
	software := shown["software"].(map[string]any)
	if shown["plan"] != nil || software["review"] != nil || shown["create_available"] == true {
		t.Fatalf("showing the folder planned or reviewed a setup: %v", shown)
	}

	offer := f.review(t)
	software = offer["software"].(map[string]any)
	review := software["review"].(map[string]any)
	if strs(review["stack"]) != "Go,Node.js" || strs(review["guides"]) != "AGENTS.md" ||
		strs(review["files"]) != "AGENTS.md,README.md,go.mod,package.json,Makefile,.github/workflows/ci.yml" {
		t.Fatalf("what the review read: %v", review)
	}
	git := review["git"].(map[string]any)
	if git["layout"] != "repository" || git["branch"] != "main" || git["remote"] != "example.invalid/acme/widget.git" {
		t.Fatalf("git = %v", git)
	}
	if review["purpose"] != "Widget is a small service that counts widgets and shows them on a web page." || review["evidence_held"] != true {
		t.Fatalf("evidence = %v", review)
	}

	lines := planLines(offer)
	want := []struct{ kind, name string }{
		{"workspace", "Creates a Code project workspace named widget"},
		{"folder", "Linked where it is; nothing is moved, copied or run"},
		{"agents", "Adds one development assistant, Development Assistant"},
		{"mode", "Starts with understanding and planning"},
		{"task", "An optional read-only summary, available for you to start after setup"},
	}
	if len(lines) != len(want) {
		t.Fatalf("plan lines = %v", lines)
	}
	for i, line := range lines {
		if line["kind"] != want[i].kind || line["name"] != want[i].name {
			t.Errorf("line %d = %v, want %s %q", i, line, want[i].kind, want[i].name)
		}
		// Nothing is installed, so nothing in the plan is a plugin line.
		if line["kind"] == "integration" || line["kind"] == "provider" || line["kind"] == "home" {
			t.Errorf("a built-in plan carries a %v line", line["kind"])
		}
	}
	if !strings.Contains(lines[1]["detail"].(string), "Read-only.") || !strings.Contains(lines[3]["detail"].(string), "AGENTS.md") ||
		!strings.Contains(lines[4]["detail"].(string), "Model tokens are spent only then; nothing in the project is run.") {
		t.Fatalf("plan details: %v", lines)
	}
	// With no model set, the assistant's line and the prerequisite both say so.
	if !strings.Contains(lines[2]["detail"].(string), "has no model yet") {
		t.Fatalf("assistant line without a model: %v", lines[2])
	}
	// The card names the hire from the blueprint's roster, and its question
	// starts from the blueprint's own goal.
	plan := offer["plan"].(map[string]any)
	hire, _ := plan["hire"].(map[string]any)
	if hire == nil || hire["mode"] != "create" || hire["agent_name"] != "Development Assistant" ||
		hire["role_label"] != "Code project" || hire["model"] != nil {
		t.Fatalf("hire = %v", plan["hire"])
	}
	if plan["brief_default"] != "Understand this project, identify evidence-backed risks, and propose prioritized improvements before changing code." {
		t.Fatalf("brief_default = %v", plan["brief_default"])
	}
	for id, state := range map[string]string{"folder": "verified", "model": "absent", "github": "absent", "execution_tool": "absent"} {
		row := prerequisite(offer, id)
		if row == nil || row["state"] != state || row["source"] == "" || row["detail"] == "" {
			t.Errorf("prerequisite %s = %v, want %s with a source and a sentence", id, row, state)
		}
	}
	if prerequisite(offer, "model")["required"] != true || prerequisite(offer, "github")["required"] != false ||
		prerequisite(offer, "model")["route"] != "/settings#system-model" || prerequisite(offer, "github")["route"] != "/settings#github-account" {
		t.Fatalf("prerequisite routes or requirement: %v %v", prerequisite(offer, "model"), prerequisite(offer, "github"))
	}
	// The production host can carry this plan out, so the card may offer it.
	if software["setup_available"] != true {
		t.Fatalf("setup_available = %v", software["setup_available"])
	}
	digest := offer["plan"].(map[string]any)["digest"].(string)
	// A plain yes is still refused: the setup is confirmed on its reviewed plan.
	if status, body := f.post(t, "decide", map[string]any{"request_id": "plain-yes", "decision": "yes", "choice": "project", "create": true}); status != http.StatusConflict {
		t.Fatalf("a plain yes on a software project: %d %v", status, body)
	}

	// Nothing was created, installed or spent, and the project is untouched.
	encoded := mustJSON(t, offer)
	for _, forbidden := range []string{os.Getenv("HOME"), "never-read-this", "node_modules", `"key"`} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("the reviewed offer carries %q", forbidden)
		}
	}
	if got := len(f.builder.workspaceFileStore.CachedWorkspaces()); got != workspaces {
		t.Fatalf("a review changed the workspaces: %d -> %d", workspaces, got)
	}
	if agentsAfter := f.agentNames(t); agentsAfter != agentsBefore || strings.Contains(agentsAfter, "Development Assistant") {
		t.Fatalf("a review created an agent: %q -> %q", agentsBefore, agentsAfter)
	}
	if len(f.provider.requests) != 0 {
		t.Fatalf("a review made %d model calls", len(f.provider.requests))
	}
	if after, err := softwarefixture.Hash(f.dir); err != nil || after != before {
		t.Fatalf("a review changed the project: %v", err)
	}
	if stored := f.read(t); stored["status"] != "pending" || stored["plan"].(map[string]any)["digest"] != digest {
		t.Fatalf("the reviewed offer was decided or its plan moved: %v", stored["status"])
	}
}

// The plan follows the model and the names as they are now, and a change to
// either is a different digest.
func TestSoftwareReview_ModelAndNamesAreReadLiveIntoThePlan(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	first := f.review(t)
	digest := first["plan"].(map[string]any)["digest"]

	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	withModel := f.read(t)
	if detail := planLines(withModel)[2]["detail"].(string); !strings.Contains(detail, "sonnet") || !strings.Contains(detail, "claude_code") {
		t.Fatalf("assistant line with a model: %q", detail)
	}
	model := prerequisite(withModel, "model")
	if model["state"] != "configured" || !strings.Contains(model["detail"].(string), "has not been called") || model["source"] != "Ori's system model setting" {
		t.Fatalf("model prerequisite: %v", model)
	}
	modelDigest := withModel["plan"].(map[string]any)["digest"]
	if modelDigest == digest {
		t.Fatal("a newly configured model kept the old digest")
	}

	status, body := f.post(t, "adjust", map[string]any{"request_id": "rename", "workspace_name": "Widget service", "assistant_name": "Pat"})
	if status != http.StatusOK {
		t.Fatalf("adjust: %d %v", status, body)
	}
	adjusted := body["offer"].(map[string]any)
	lines := planLines(adjusted)
	if lines[0]["name"] != "Creates a Code project workspace named Widget service" || lines[1]["name"] != "Linked where it is; nothing is moved, copied or run" ||
		lines[2]["name"] != "Adds one development assistant, Pat" || adjusted["plan"].(map[string]any)["digest"] == modelDigest {
		t.Fatalf("adjusted plan: %v", lines)
	}
	// A renamed assistant is the one the card hires, on the model it will use.
	if hire, _ := adjusted["plan"].(map[string]any)["hire"].(map[string]any); hire == nil || hire["agent_name"] != "Pat" ||
		hire["mode"] != "create" || hire["model"] != "sonnet" {
		t.Fatalf("renamed hire = %v", adjusted["plan"].(map[string]any)["hire"])
	}
	for _, bad := range []map[string]any{
		{"request_id": "bad-1", "workspace_name": "a/b"},
		{"request_id": "bad-2", "assistant_name": "../up"},
	} {
		if status, _ := f.post(t, "adjust", bad); status != http.StatusBadRequest {
			t.Errorf("adjust %v => %d", bad, status)
		}
	}
	if len(f.provider.requests) != 0 || len(f.builder.workspaceFileStore.CachedWorkspaces()) == 0 {
		t.Fatalf("reviewing and adjusting called a model (%d calls)", len(f.provider.requests))
	}
}

// Repository text is data. A README that gives orders and carries a
// credential-shaped value changes nothing about the plan, and the value never
// reaches the card.
func TestSoftwareReview_HostileProjectTextIsOnlyData(t *testing.T) {
	f := newSoftwareCardFixture(t, "hostile")
	offer := f.review(t)
	encoded := mustJSON(t, offer)
	if strings.Contains(encoded, softwarefixture.CredentialShapedValue()) || strings.Contains(encoded, "HOSTILE_FIXTURE_TOKEN") ||
		strings.Contains(encoded, "deploy:") {
		t.Fatal("a credential-shaped value or the remote's user reached the card")
	}
	review := offer["software"].(map[string]any)["review"].(map[string]any)
	if review["git"].(map[string]any)["remote"] != "example.invalid/acme/hostile.git" {
		t.Fatalf("remote = %v", review["git"])
	}
	lines := planLines(offer)
	if len(lines) != 5 || lines[0]["name"] != "Creates a Code project workspace named hostile" {
		t.Fatalf("plan = %v", lines)
	}
	for _, line := range lines {
		text := line["name"].(string) + " " + line["detail"].(string)
		if strings.Contains(strings.ToLower(text), "deploy") || strings.Contains(text, "IGNORE") {
			t.Fatalf("project text shaped the plan: %q", text)
		}
	}
	// The declared deploy script is not offered as a build or test command.
	if commands := mustJSON(t, review["commands"]); strings.Contains(commands, "deploy") || !strings.Contains(commands, "npm run test") {
		t.Fatalf("commands = %s", commands)
	}
	if len(f.provider.requests) != 0 {
		t.Fatal("reviewing a hostile project called a model")
	}
}

// A folder replaced after it was shown is not the folder that was reviewed:
// the plan goes away and the card asks for the folder again.
func TestSoftwareReview_AReplacedFolderLosesItsPlan(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget-clone")
	if offer := f.review(t); offer["plan"] == nil {
		t.Fatalf("no plan to lose: %v", offer)
	}
	if err := softwarefixture.Replace(f.dir); err != nil {
		t.Fatal(err)
	}
	stale := f.read(t)
	if stale["needs_pick"] != true || stale["plan"] != nil {
		t.Fatalf("a replaced folder kept its plan: needs_pick=%v plan=%v", stale["needs_pick"], stale["plan"])
	}
	if status, body := f.post(t, "review", map[string]any{"request_id": "review-2"}); status != http.StatusConflict || body["needs_pick"] != true {
		t.Fatalf("a review of a replaced folder: %d %v", status, body)
	}
}
