package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// seedAlbumsCollection lays out, in the sandboxed Music folder, the collection
// the feature was reported on: five album folders that each hold a session file
// and its media, and one sketch folder no marker names. Every file is two weeks
// old. It returns the folder.
func seedAlbumsCollection(t *testing.T) string {
	t.Helper()
	return seedAlbumsCollectionVariant(t, false)
}

func seedAlbumsCollectionVariant(t *testing.T, exact bool) string {
	t.Helper()
	root := filepath.Join(os.Getenv("HOME"), "Music") // sandboxed by the fixture, never the user's home
	write := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("source must stay unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().AddDate(0, 0, -14)
		if err := os.Chtimes(full, at, at); err != nil {
			t.Fatal(err)
		}
	}
	for n := 1; n <= 5; n++ {
		write(fmt.Sprintf("Album-%d/Song.rpp", n))
		if !exact {
			write(fmt.Sprintf("Album-%d/Media/drums.wav", n))
		}
	}
	write("Album-3/Song.rpp-bak")
	write("Album-5/Alternate.rpp")
	if exact {
		write("Logic Sketch/Sketch.logicx/ProjectData")
	} else {
		for _, name := range []string{"ideas.txt", "sketch.mid", "bounce.wav"} {
			write("Logic Sketch/" + name)
		}
	}
	return root
}

// assertProposedAlbums checks a structure as a browser receives it: the whole
// collection proposed as a plain group, because this server has neither the
// Home's provider nor the integration installed. Both are known from what is on
// this computer, so the answer does not depend on a release lookup.
func assertProposedAlbums(t *testing.T, structure map[string]any, root string) {
	t.Helper()
	group := structure["group"].(map[string]any)
	if group["type"] != "group" || group["name"] != "Music" || group["destination_state"] != "new" ||
		group["home_name"] != "Music Production Home" || group["availability"] != "provider_not_installed" || group["home_available"] != true {
		t.Fatalf("group: %v", group)
	}
	members := structure["members"].([]any)
	sessions := 0
	for _, item := range members {
		member := item.(map[string]any)
		if member["included"] != true || member["name"] == "" || member["candidate_id"] == "" {
			t.Fatalf("member: %v", member)
		}
		if member["type_label"] == "REAPER session" {
			sessions++
			if member["availability"] != "integration_not_installed" || member["blueprint_id"] != nil {
				t.Fatalf("a session with nothing installed must be a plain workspace for now: %v", member)
			}
		} else if member["type_label"] != "Plain workspace" || member["name"] != "Logic Sketch" || member["availability"] != nil {
			t.Fatalf("plain member: %v", member)
		}
	}
	if len(members) != 6 || sessions != 5 || structure["projects"] != float64(5) {
		t.Fatalf("%d members, %d sessions, projects %v", len(members), sessions, structure["projects"])
	}
	notes := structure["notes"].([]any)
	if len(notes) != 2 || notes[0] != "Music Production Home is not installed; a plain group is proposed instead." ||
		notes[1] != "REAPER integration is not installed; it can be added later." {
		t.Fatalf("notes: %v", notes)
	}
	encoded, err := json.Marshal(structure)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{root, os.Getenv("HOME"), "rel_path", "identity", "marker_name"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the structure carries %q: %s", forbidden, encoded)
		}
	}
}

// folderTreeHash is every file under root: its name, size, time and bytes. A
// setup that links a folder where it is leaves this unchanged.
func folderTreeHash(t *testing.T, root string) string {
	t.Helper()
	sum := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		// A hash never fails a write.
		_, _ = fmt.Fprintf(sum, "%s|%d|%d|%v\n", rel, info.Size(), info.ModTime().UnixNano(), info.Mode())
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path) // #nosec G304 -- a fixture file under the test's sandboxed home
			if err != nil {
				return err
			}
			sum.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// albumsReview is one conversation's review of the seeded collection.
type albumsReview struct {
	conversation, revision, selection, candidate, offer string
	// view is the review card as the conversation shows it.
	view map[string]any
}

// reviewAlbums attaches the seeded collection in a new conversation draft and
// opens its review, as "Review group setup" does.
func reviewAlbums(t *testing.T, f *draftServerFixture) albumsReview {
	t.Helper()
	draft := uuid.NewString()
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK || body["structure"] == nil {
		t.Fatalf("select: %d %v", status, body)
	}
	observation := body["observation"].(map[string]any)
	review := albumsReview{selection: observation["id"].(string)}
	for _, item := range observation["projects"].([]any) {
		if project := item.(map[string]any); project["root"] == true {
			review.candidate = project["id"].(string)
		}
	}
	review.conversation, review.revision, review.offer = reviewFolder(t, f, map[string]any{
		"draft_id": draft, "revision": "", "selection_id": review.selection, "candidate_id": review.candidate,
		"structure": map[string]any{"group_type": "group"}, // explicit generic alternative
	})
	review.view = review.read(t, f)
	return review
}

func (r albumsReview) read(t *testing.T, f *draftServerFixture) map[string]any {
	t.Helper()
	status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+r.conversation, nil)
	if status != http.StatusOK {
		t.Fatalf("conversation: %d %v", status, history)
	}
	return history["folder_reviews"].(map[string]any)[r.offer].(map[string]any)
}

// adjust sends the sheet's choices to the same review, as Done does.
func (r albumsReview) adjust(t *testing.T, f *draftServerFixture, adjustment map[string]any) albumsReview {
	t.Helper()
	conversation, revision, offer := reviewFolder(t, f, map[string]any{
		"conversation_id": r.conversation, "revision": r.revision, "selection_id": r.selection,
		"candidate_id": r.candidate, "structure": adjustment,
	})
	if conversation != r.conversation || offer != r.offer {
		t.Fatalf("adjusting opened another review: %s/%s", conversation, offer)
	}
	r.revision = revision
	r.view = r.read(t, f)
	return r
}

// setUpAndWait presses Set up on a reviewed plan and waits for the offer to
// settle. The run is the server's own; nothing here drives it.
func setUpAndWait(t *testing.T, f *draftServerFixture, offer string, view map[string]any, requestID string) map[string]any {
	t.Helper()
	plan, ok := view["plan"].(map[string]any)
	if !ok {
		t.Fatalf("the review has no plan: %v", view)
	}
	path := "/api/personal-assistant/folder-digest/offers/" + offer + "/setup"
	if status, body := f.call(t, http.MethodPost, path, map[string]any{"request_id": requestID, "plan_digest": plan["digest"]}); status != http.StatusOK {
		t.Fatalf("set up: %d %v", status, body)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, body := f.call(t, http.MethodGet, "/api/personal-assistant/folder-digest?offer_id="+offer, nil)
		if status != http.StatusOK {
			t.Fatalf("read offer: %d %v", status, body)
		}
		digest, _ := body["folder_digest"].(map[string]any)
		current, ok := digest["offer"].(map[string]any)
		if !ok {
			t.Fatalf("the offer could not be read back: %v", body)
		}
		if current["status"] == "resolved" {
			return current
		}
		if setup, ok := current["setup"].(map[string]any); ok && setup["status"] == "stopped" {
			t.Fatalf("the setup stopped: %v", setup)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the setup did not finish: %v", current)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func planLineNames(view map[string]any) []string {
	var names []string
	for _, item := range view["plan"].(map[string]any)["lines"].([]any) {
		line := item.(map[string]any)
		names = append(names, line["kind"].(string)+": "+line["name"].(string))
	}
	return names
}

func collectionReceiptRows(outcome map[string]any, kind string) []any {
	var rows []any
	for _, row := range outcome["receipt"].([]any) {
		if row.(map[string]any)["kind"] == kind {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestAssistantFolderCollection_OneReviewOneRunCreatesTheGroupAndItsWorkspaces(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	root := seedAlbumsCollection(t)
	hash := folderTreeHash(t, root)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())

	review := reviewAlbums(t, f)
	offer, view := review.offer, review.view
	want := []string{
		"group: Creates group Music",
		"workspaces: Creates 6 workspaces, each linked to its own folder",
		"member: Album-1", "member: Album-2", "member: Album-3", "member: Album-4", "member: Album-5", "member: Logic Sketch",
		"folder: Nothing is moved, copied or read",
		"unavailable: Music Production Home is not installed; a plain group is proposed instead.",
		"unavailable: REAPER integration is not installed; it can be added later.",
		"task: No task is created or started",
	}
	if got := planLineNames(view); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan:\n%s", strings.Join(got, "\n"))
	}
	if len(files.CachedWorkspaces()) != before {
		t.Fatal("reviewing created a workspace")
	}

	resolved := setUpAndWait(t, f, offer, view, "set-up-once")
	outcome := resolved["outcome"].(map[string]any)
	receipt := collectionReceiptRows(outcome, "workspace")
	if len(receipt) != 7 || receipt[0].(map[string]any)["detail"] != "group" || receipt[0].(map[string]any)["name"] != "Music" {
		t.Fatalf("receipt: %v", receipt)
	}
	group, err := files.Get(outcome["workspace_id"].(string))
	if err != nil || group.Kind != "group" || group.Name != "Music" || outcome["route"] != "/workspaces/"+group.FolderSlug {
		t.Fatalf("group: %+v, %v", group, err)
	}
	agents := collectionReceiptRows(outcome, "agent")
	if len(agents) != 1 || len(group.AgentInstances) != 1 || len(outcome["receipt"].([]any)) != len(receipt)+len(agents) {
		t.Fatalf("membership receipt does not match the group: %v, %+v", agents, group.AgentInstances)
	}
	member := agents[0].(map[string]any)
	_, model, found := f.builder.sessionHandler.SavedAgentModel(group.AgentInstances[0].Name)
	if member["assigned"] != true || member["name"] != group.AgentInstances[0].Name || !found || member["model"] != model {
		t.Fatalf("membership is not a canonical fact: %v", member)
	}
	children := map[string]*workspace.Workspace{}
	for _, ws := range files.CachedWorkspaces() {
		if ws != nil && ws.ParentID == group.ID {
			children[ws.Name] = ws
		}
	}
	if len(children) != 6 || len(files.CachedWorkspaces()) != before+7 {
		t.Fatalf("%d children, %d workspaces in all (was %d)", len(children), len(files.CachedWorkspaces()), before)
	}
	// The server holds canonical paths; the sandboxed home may sit behind a link.
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, ws := range children {
		primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
		ref, err := ws.GetDirectoryReference(primary)
		if primary == "" || err != nil || ref.Path != filepath.Join(canonical, name) || ws.Kind == "group" {
			t.Fatalf("%s is not linked to its own folder: %+v, %v", name, ref, err)
		}
		// A plain workspace made for a recognized folder remembers what it was
		// recognized as, for a later integration to find.
		shape, _ := ws.SharedData[folderShapeKey].(string)
		marker, _ := ws.SharedData[folderMarkerKey].(string)
		if session := strings.HasPrefix(name, "Album-"); (session && (shape != "audio" || marker != "*.rpp")) || (!session && (shape != "" || marker != "")) {
			t.Fatalf("%s recorded shape %q, marker %q", name, shape, marker)
		}
		// The plan said no task is created, so none is waiting in any of them.
		if len(ws.Tasks) != 0 {
			t.Fatalf("%s was given %d tasks by the setup", name, len(ws.Tasks))
		}
	}
	// The folder is exactly as it was, and no model was asked anything.
	if folderTreeHash(t, root) != hash {
		t.Fatal("the attached folder changed")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("the setup made %d model calls", len(provider.requests))
	}

	// The same confirmed click again returns what was done and creates nothing.
	path := "/api/personal-assistant/folder-digest/offers/" + offer + "/setup"
	status, replay := f.call(t, http.MethodPost, path, map[string]any{"request_id": "set-up-once", "plan_digest": view["plan"].(map[string]any)["digest"]})
	if status != http.StatusOK || replay["offer"].(map[string]any)["status"] != "resolved" || len(files.CachedWorkspaces()) != before+7 {
		t.Fatalf("replay: %d %v; %d workspaces", status, replay["offer"], len(files.CachedWorkspaces()))
	}
	// Attaching the folder again proposes no second group: every folder in it
	// is already a workspace.
	status, again := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "music"})
	if _, proposed := again["structure"]; status != http.StatusOK || proposed {
		t.Fatalf("a second group was proposed for the same folders: %d %v", status, again["structure"])
	}
}

func TestAssistantFolderCollection_ASecondFolderWithTheSameNamesIsSetUpBesideTheFirst(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	first := reviewAlbums(t, f)
	setUpAndWait(t, f, first.offer, first.view, "first-folder")
	before := len(files.CachedWorkspaces())

	// A second folder whose projects are called the same as the first one's.
	// Workspace names share one namespace, so the proposal names them apart.
	documents := filepath.Join(os.Getenv("HOME"), "Documents")
	for _, name := range []string{"Album-1", "Album-2"} {
		if err := os.MkdirAll(filepath.Join(documents, name), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(documents, name, "Song.rpp"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	draft := uuid.NewString()
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "documents"})
	structure, ok := body["structure"].(map[string]any)
	if status != http.StatusOK || !ok {
		t.Fatalf("select: %d %v", status, body)
	}
	names := map[string]any{}
	for _, item := range structure["members"].([]any) {
		member := item.(map[string]any)
		names[member["folder"].(string)] = member["name"]
	}
	if structure["group"].(map[string]any)["name"] != "Documents" || names["Album-1"] != "Album-1 (Documents)" || names["Album-2"] != "Album-2 (Documents)" {
		t.Fatalf("second proposal: group %v, members %v", structure["group"], names)
	}
	observation := body["observation"].(map[string]any)
	root := ""
	for _, item := range observation["projects"].([]any) {
		if project := item.(map[string]any); project["root"] == true {
			root = project["id"].(string)
		}
	}
	conversation, _, offer := reviewFolder(t, f, map[string]any{"draft_id": draft, "revision": "", "selection_id": observation["id"], "candidate_id": root})
	second := albumsReview{conversation: conversation, offer: offer}
	resolved := setUpAndWait(t, f, offer, second.read(t, f), "second-folder")
	if rows := collectionReceiptRows(resolved["outcome"].(map[string]any), "workspace"); len(rows) != 3 || len(files.CachedWorkspaces()) != before+3 {
		t.Fatalf("second setup: %v; %d workspaces (was %d)", rows, len(files.CachedWorkspaces()), before)
	}
}

func TestAssistantFolderCollection_AdjustedGroupCreatesOnlyWhatWasChosen(t *testing.T) {
	f := newDraftServerFixture(t)
	root := seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())
	proposed := reviewAlbums(t, f)
	// The sheet's choices: every member by the id the proposal gave it, the
	// sketch unchecked, the group renamed.
	var members []any
	for _, item := range proposed.view["structure"].(map[string]any)["members"].([]any) {
		member := item.(map[string]any)
		members = append(members, map[string]any{
			"candidate_id": member["candidate_id"], "name": member["name"], "included": member["name"] != "Logic Sketch",
		})
	}
	adjusted := proposed.adjust(t, f, map[string]any{"group_type": "group", "group_name": "Albums 2026", "members": members})
	offer, view := adjusted.offer, adjusted.view
	lines := planLineNames(view)
	if lines[0] != "group: Creates group Albums 2026" || lines[1] != "workspaces: Creates 5 workspaces, each linked to its own folder" ||
		strings.Contains(strings.Join(lines, "\n"), "Logic Sketch") {
		t.Fatalf("adjusted plan:\n%s", strings.Join(lines, "\n"))
	}
	// The plan the user confirms is the adjusted one: a click made on the
	// first proposal's plan is refused, and nothing is created.
	stale := proposed.view["plan"].(map[string]any)["digest"]
	if view["plan"].(map[string]any)["digest"] == stale {
		t.Fatal("adjusting did not re-stamp the plan")
	}
	path := "/api/personal-assistant/folder-digest/offers/" + offer + "/setup"
	if status, body := f.call(t, http.MethodPost, path, map[string]any{"request_id": "stale-plan", "plan_digest": stale}); status != http.StatusConflict ||
		len(files.CachedWorkspaces()) != before {
		t.Fatalf("a stale plan was accepted: %d %v", status, body)
	}
	resolved := setUpAndWait(t, f, offer, view, "adjusted-once")
	outcome := resolved["outcome"].(map[string]any)
	group, err := files.Get(outcome["workspace_id"].(string))
	if err != nil || group.Name != "Albums 2026" || len(collectionReceiptRows(outcome, "workspace")) != 6 || len(files.CachedWorkspaces()) != before+6 {
		t.Fatalf("adjusted setup: %+v, %v; %d workspaces (was %d)", group, err, len(files.CachedWorkspaces()), before)
	}
	for _, ws := range files.CachedWorkspaces() {
		if ws != nil && ws.ParentID == group.ID && ws.Name == "Logic Sketch" {
			t.Fatal("the unchecked folder got a workspace")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Logic Sketch", "ideas.txt")); err != nil {
		t.Fatalf("the unchecked folder was touched: %v", err)
	}
}

func TestAssistantFolderCollection_AttachAndScanCarryTheProposedStructure(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	root := seedAlbumsCollection(t)
	sourceHash := folderTreeHash(t, root)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())

	// Attaching in the composer: the proposal arrives with the observation,
	// before any message is sent.
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, body)
	}
	structure, ok := body["structure"].(map[string]any)
	if !ok {
		t.Fatalf("the attach response carries no structure: %v", body)
	}
	recommendation := structure["recommendation"].(map[string]any)
	destination := recommendation["destination"].(map[string]any)
	if recommendation["operation"] != "library" || recommendation["supported_projects"] != float64(5) ||
		destination["state"] != "absent" || destination["readiness"] != "provider_missing" {
		t.Fatalf("local library recommendation: %v", recommendation)
	}

	// A single folder attaches exactly as before, with no structure.
	single := filepath.Join(os.Getenv("HOME"), "Desktop")
	if err := os.MkdirAll(single, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(single, "todo.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, body = f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "desktop"})
	if _, present := body["structure"]; status != http.StatusOK || present {
		t.Fatalf("a single folder was proposed as a group: %d %v", status, body)
	}

	// Home's Explore a folder: the scan's offer carries the same record.
	status, body = f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/scan", map[string]any{"chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("scan: %d %v", status, body)
	}
	offer := body["offer"].(map[string]any)
	scanned, ok := offer["structure"].(map[string]any)
	if !ok {
		t.Fatalf("the scan's offer carries no structure: %v", offer)
	}
	if scanned["group"].(map[string]any)["type"] != "library" || scanned["recommendation"] == nil || offer["portfolio"] == nil || offer["plan"] != nil {
		t.Fatalf("Home must retain library recommendation and disclose prerequisites: %v", offer)
	}
	if folderTreeHash(t, root) != sourceHash {
		t.Fatal("discovery changed source files")
	}

	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(provider.requests) != 0 {
		t.Fatal("describing a collection created a workspace or called a model")
	}
}
