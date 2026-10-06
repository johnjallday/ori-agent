package progression

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	ws "github.com/johnjallday/ori-agent/internal/workspace"
)

// The built-in graph serves every non-cohort caller and the tests above. The
// starter missions re-tier only the cohort graph, so this golden pins the
// built-in one: IDs, tiers, titles, destinations, and optionality in order.
func TestBuiltinGraph_IsUnchanged(t *testing.T) {
	want := []string{
		"t1-first-message|1|Send your first request||false",
		"t1-personalize|1|Personalize Ori|/profile#personalization|false",
		"t2-create-workspace|2|Create your first workspace|/?create=1|false",
		"t2-build-hq|2|Build My HQ|/?focus=personal-hq|true",
		"t2-create-note|2|Write a note||false",
		"t2-run-task|2|Run your first task||false",
		"t3-second-agent|3|Add a second agent||false",
		"t3-delegate|3|Delegate a task to an agent||false",
		"t3-agent-task-done|3|See an agent finish a task||false",
		"t4-enable-skill|4|Enable a skill||false",
		"t4-connect-mcp|4|Connect an MCP server||false",
		"t4-tool-task|4|Run a task that uses a tool||false",
		"t5-create-trigger|5|Set up a trigger or schedule||false",
		"t5-unattended-run|5|Get your first unattended result||false",
		"t6-orchestrate|6|Run a multi-agent orchestration||false",
		"t6-memory|6|Write to workspace memory||false",
	}
	graph := BuiltinGraph()
	var got []string
	for _, q := range graph.Quests {
		if q.Featured || q.Order != 0 || q.Resolve != nil {
			t.Fatalf("built-in quest %s gained starter-mission fields", q.ID)
		}
		got = append(got, fmt.Sprintf("%s|%d|%s|%s|%t", q.ID, q.Tier, q.Title, q.ActionURL, q.Optional))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("built-in graph changed:\n%s", strings.Join(got, "\n"))
	}

	names := []string{"First Contact", "Establish a Base", "Recruit", "Equip", "Automate", "Command"}
	for i, name := range names {
		if graph.TierNames[i+1] != name || TierName(i+1) != name {
			t.Fatalf("tier %d name = %q / %q, want %q", i+1, graph.TierNames[i+1], TierName(i+1), name)
		}
	}
	if graph.TotalTiers != TotalTiers {
		t.Fatalf("built-in total tiers = %d", graph.TotalTiers)
	}
}

func TestPersonalAssistantGraph_Shape(t *testing.T) {
	graph := PersonalAssistantGraph()

	wantTiers := map[string]int{
		MeetAssistantQuestID: 1,
		BuildHQQuestID:       1, ShowFolderQuestID: 1, FolderFirstLookQuestID: 1,
		ConnectSourceQuestID: 1, FirstBriefQuestID: 1,
		"t1-first-message": 2, "t1-personalize": 2, "t2-create-note": 2, "t2-run-task": 2,
		"t3-second-agent": 3, "t3-delegate": 3, "t3-agent-task-done": 3,
		"t4-enable-skill": 4, "t4-connect-mcp": 4, "t4-tool-task": 4,
		"t5-create-trigger": 5, "t5-unattended-run": 5,
		"t6-orchestrate": 6, "t6-memory": 6,
	}
	if len(graph.Quests) != len(wantTiers) {
		t.Fatalf("cohort graph has %d quests, want %d", len(graph.Quests), len(wantTiers))
	}
	seen := map[string]bool{}
	for _, q := range graph.Quests {
		tier, ok := wantTiers[q.ID]
		if !ok {
			t.Fatalf("unexpected quest %q in the cohort graph", q.ID)
		}
		if seen[q.ID] {
			t.Fatalf("quest %q appears twice", q.ID)
		}
		seen[q.ID] = true
		if q.Tier != tier {
			t.Fatalf("quest %q tier = %d, want %d", q.ID, q.Tier, tier)
		}
	}
	for _, retired := range []string{PersonalAssistantFirstDayQuestID, "t2-create-workspace", TidyDownloadsQuestID} {
		if seen[retired] {
			t.Fatalf("retired quest %q is still in the cohort graph", retired)
		}
	}

	// Exactly five missions stay visible. Retired objectives still have IDs,
	// detectors, and completions but cannot appear on the mission board.
	wantMissions := []struct {
		id, title, url, label string
		optional              bool
		lockedUntil           string
	}{
		{MeetAssistantQuestID, "Meet your assistant", MeetAssistantActionURL, "Start", false, ""},
		{ShowFolderQuestID, "Show your assistant a folder", ShowFolderActionURL, "Start", true, BuildHQQuestID},
		// The static action is the no-folder fallback; Resolve replaces it once
		// a folder workspace has a first look.
		{FolderFirstLookQuestID, "See what your assistant found", ShowFolderActionURL, "Show a folder", true, ShowFolderQuestID},
		{ConnectSourceQuestID, "Plan my first day", PlanFirstDayActionURL, "Start", true, MeetAssistantQuestID},
		{FirstBriefQuestID, "Read your first Daily Brief", FirstBriefFallbackURL, "Open Daily Brief", true, MeetAssistantQuestID},
	}
	for i, want := range wantMissions {
		q := graph.Quests[i]
		if i > 0 {
			q = graph.Quests[i+1]
		} // the retired HQ quest keeps its original position
		if q.ID != want.id || q.Order != i+1 || !q.Featured || q.Retired || q.Optional != want.optional {
			t.Fatalf("mission %d = %s order %d featured %t optional %t", i+1, q.ID, q.Order, q.Featured, q.Optional)
		}
		if q.Title != want.title || q.ActionURL != want.url || q.ActionLabel != want.label {
			t.Fatalf("mission %s copy = %q %q %q", q.ID, q.Title, q.ActionURL, q.ActionLabel)
		}
		if q.LockedUntil != want.lockedUntil {
			t.Fatalf("mission %s locked until %q, want %q", q.ID, q.LockedUntil, want.lockedUntil)
		}
		if strings.TrimSpace(q.Why) == "" {
			t.Fatalf("mission %s has no why line", q.ID)
		}
	}
	if why := graph.Quests[0].Why; why != "Your assistant is the one agent that owns your ongoing work. Make them yours." {
		t.Fatalf("Meet your assistant why = %q", why)
	}
	if why := graph.Quests[2].Why; why != "Point Ori at a folder and it will tell you what it can do with it." {
		t.Fatalf("Show your assistant a folder why = %q", why)
	}
	firstLook := graph.Quests[3]
	if firstLook.Why != "Your assistant takes one read-only look at the folder and reports back. This is where you see what it can do." {
		t.Fatalf("See what your assistant found why = %q", firstLook.Why)
	}
	// It completes from the server's task hook, never from an event Match, and
	// is grandfathered by a first look that already finished.
	if firstLook.Match != nil || firstLook.Satisfied == nil || firstLook.Resolve == nil {
		t.Fatalf("See what your assistant found wiring: match=%t satisfied=%t resolve=%t",
			firstLook.Match != nil, firstLook.Satisfied != nil, firstLook.Resolve != nil)
	}
	for _, q := range graph.Quests {
		if q.Featured {
			continue
		}
		if !q.Retired || q.Order != 0 {
			t.Fatalf("non-mission quest %s is not retired or has order %d", q.ID, q.Order)
		}
	}
	if q := graph.Quests[1]; q.ID != BuildHQQuestID || q.Featured || !q.Retired || q.ActionURL != GuidedBuildHQActionURL {
		t.Fatalf("HQ quest must keep its Map action but not its mission slot: %+v", q)
	}

	names := []string{"Starter", "Daily loop", "Recruit", "Equip", "Automate", "Command"}
	for i, name := range names {
		if graph.TierNames[i+1] != name {
			t.Fatalf("cohort tier %d = %q, want %q", i+1, graph.TierNames[i+1], name)
		}
	}
	if graph.TotalTiers != 1 {
		t.Fatalf("cohort total tiers = %d", graph.TotalTiers)
	}
	// Renaming the cohort's tiers must not leak into the built-in names.
	if TierName(1) != "First Contact" {
		t.Fatalf("built-in tier 1 renamed to %q", TierName(1))
	}
}

func TestPersonalAssistantGraph_RetiredQuestsKeepHistoryWithoutMissionsOrRewards(t *testing.T) {
	graph := PersonalAssistantGraph()
	visible := []string{}
	for _, q := range graph.Quests {
		if !q.Retired {
			visible = append(visible, q.ID)
		}
	}
	want := []string{MeetAssistantQuestID, ShowFolderQuestID, FolderFirstLookQuestID, ConnectSourceQuestID, FirstBriefQuestID}
	if strings.Join(visible, ",") != strings.Join(want, ",") {
		t.Fatalf("visible quests = %v, want %v", visible, want)
	}
	e := New(&fakeStore{}, WithGraph(graph))
	before := e.Status()
	// A retired quest still matches its event but cannot change the status meter.
	e.HandleEvent(ws.Event{Type: ws.EventMessageSent})
	if !e.HasCompleted("t1-first-message") {
		t.Fatal("a retired quest stopped recording real events")
	}
	if after := e.Status(); after.CompletedCount != before.CompletedCount || after.TotalCount != 5 || after.TotalTiers != 1 || after.NextQuest.ID != MeetAssistantQuestID {
		t.Fatalf("retired completion changed the mission board: %+v", after)
	}
	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	if e.HasCompleted("t1-first-message") || e.Status().TotalCount != 5 {
		t.Fatal("reset did not clear retired history while preserving the visible graph")
	}
}

func TestStatus_UsesTheGraphsTierNames(t *testing.T) {
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	st := e.Status()
	if len(st.Tiers) != 1 || st.Tiers[0].Name != "Starter" {
		t.Fatalf("visible tiers = %+v", st.Tiers)
	}
	if st.TotalTiers != 1 || st.CurrentTier != 1 {
		t.Fatalf("total/current = %d/%d", st.TotalTiers, st.CurrentTier)
	}

	// Resolving every mission completes the only visible tier. Completing the
	// retired HQ objective does not affect its status or the meter.
	for _, id := range []string{MeetAssistantQuestID, BuildHQQuestID, ShowFolderQuestID, FolderFirstLookQuestID, ConnectSourceQuestID} {
		e.Complete(id)
	}
	if err := e.Skip(FirstBriefQuestID); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(); st.CurrentTier != 1 || !st.AllComplete || st.TotalCount != 5 {
		t.Fatalf("status after five missions = %+v", st)
	}
}

func TestWithQuests_KeepsBuiltinTierNames(t *testing.T) {
	e := New(&fakeStore{}, WithQuests(PersonalAssistantQuests()))
	if name := e.Status().Tiers[0].Name; name != "First Contact" {
		t.Fatalf("WithQuests tier 1 name = %q, want the built-in name", name)
	}
}

func TestStatus_MissionsInOrderWithStatus(t *testing.T) {
	// Declared out of order on purpose: Missions sorts by Order, not position.
	graph := Graph{
		Quests: []Quest{
			{ID: "b", Tier: 1, Title: "B", Featured: true, Order: 2, Optional: true},
			{ID: "plain", Tier: 1, Title: "Plain"},
			{ID: "a", Tier: 1, Title: "A", Featured: true, Order: 1, Optional: true},
			{ID: "c", Tier: 2, Title: "C", Featured: true, Order: 3, Optional: true},
		},
		TierNames:  map[int]string{1: "One", 2: "Two"},
		TotalTiers: 2,
	}
	e := New(&fakeStore{}, WithGraph(graph))
	e.Complete("a")
	if err := e.Skip("b"); err != nil {
		t.Fatal(err)
	}

	missions := e.Status().Missions
	var got []string
	for _, m := range missions {
		got = append(got, fmt.Sprintf("%s:%d:%s:%t", m.ID, m.Order, m.Status, m.Featured))
	}
	want := "a:1:completed:true b:2:skipped:true c:3:locked-tier:true"
	if strings.Join(got, " ") != want {
		t.Fatalf("missions = %v, want %s", got, want)
	}
}

func TestStatus_MissionsEmptyForAGraphWithoutFeaturedQuests(t *testing.T) {
	st := New(&fakeStore{}).Status()
	if st.Missions == nil || len(st.Missions) != 0 {
		t.Fatalf("built-in missions = %#v, want an empty list", st.Missions)
	}
}

func TestStatus_ResolveOverlaysOnlyWhatItSets(t *testing.T) {
	var seen MissionContext
	graph := Graph{
		Quests: []Quest{
			{
				ID: "resolved", Tier: 1, Featured: true, Order: 1, Optional: true,
				Title: "Static title", Why: "Static why", ActionURL: "/static", ActionLabel: "Static",
				Resolve: func(ctx MissionContext) MissionPresentation {
					seen = ctx
					return MissionPresentation{ActionURL: "/workspaces/tidy", ActionLabel: "Finish setup", InProgress: true}
				},
			},
			{
				ID: "unchanged", Tier: 1, Featured: true, Order: 2, Optional: true,
				Title: "Keep me", ActionURL: "/keep",
				Resolve: func(MissionContext) MissionPresentation { return MissionPresentation{} },
			},
		},
		TierNames:  map[int]string{1: "One"},
		TotalTiers: 1,
	}
	provided := MissionContext{
		FocusAreas:  []string{"help_with_email"},
		FileJanitor: &MissionWorkspace{Slug: "tidy"},
	}
	e := New(&fakeStore{}, WithGraph(graph), WithMissionContext(func() MissionContext { return provided }))

	st := e.Status()
	first := st.Missions[0]
	if first.Title != "Static title" || first.Why != "Static why" {
		t.Fatalf("empty presentation fields replaced static copy: %+v", first)
	}
	if first.ActionURL != "/workspaces/tidy" || first.ActionLabel != "Finish setup" || !first.InProgress {
		t.Fatalf("resolved presentation not applied: %+v", first)
	}
	if second := st.Missions[1]; second.Title != "Keep me" || second.ActionURL != "/keep" || second.InProgress {
		t.Fatalf("zero presentation changed the quest: %+v", second)
	}
	if seen.FileJanitor == nil || seen.FileJanitor.Slug != "tidy" || len(seen.FocusAreas) != 1 {
		t.Fatalf("Resolve did not receive the provider's context: %+v", seen)
	}
	// The resolved copy is the one the tier list shows too.
	if view := questView(e, "resolved"); view == nil || view.ActionURL != "/workspaces/tidy" {
		t.Fatalf("tier view not resolved: %+v", view)
	}

	// A resolved mission is never "in progress".
	e.Complete("resolved")
	if st := e.Status(); st.Missions[0].InProgress {
		t.Fatalf("completed mission still in progress: %+v", st.Missions[0])
	}
}

func TestStatus_NoProviderResolvesWithZeroContext(t *testing.T) {
	var calls int
	graph := Graph{
		Quests: []Quest{{
			ID: "m", Tier: 1, Featured: true, Order: 1, Title: "Static",
			Resolve: func(ctx MissionContext) MissionPresentation {
				calls++
				if ctx.FileJanitor != nil || len(ctx.FocusAreas) != 0 {
					t.Errorf("zero context expected, got %+v", ctx)
				}
				return MissionPresentation{}
			},
		}},
		TierNames: map[int]string{1: "One"}, TotalTiers: 1,
	}
	New(&fakeStore{}, WithGraph(graph)).Status()
	if calls != 1 {
		t.Fatalf("Resolve calls = %d, want 1", calls)
	}
}

// The provider reads other services, and some of them complete quests from
// their own goroutines. Calling it before the engine lock means a provider
// that blocks on such a completion cannot deadlock Status.
func TestStatus_CallsTheProviderOutsideTheLock(t *testing.T) {
	var e *Engine
	provider := func() MissionContext {
		done := make(chan bool, 1)
		go func() { done <- e.Complete("t1-personalize") }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Complete blocked while the mission provider ran: the provider is called under the lock")
		}
		return MissionContext{}
	}
	e = New(&fakeStore{}, WithMissionContext(provider))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.Status()
	}()
	wg.Wait()
	if !completed(e, "t1-personalize") {
		t.Fatal("the completion made during the provider call was lost")
	}
}

// Show your assistant a folder has no resolver: the card's Start always opens
// the chooser, whatever File Janitor workspaces exist. A janitor mid-setup no
// longer turns the card into "Finish setup".
func TestShowFolder_PresentsTheSameCardWhateverTheJanitorState(t *testing.T) {
	for _, janitor := range []*MissionWorkspace{nil, {Slug: "tidy-downloads"}, {Slug: "tidy-downloads", WizardReady: true}} {
		provider := func() MissionContext { return MissionContext{FileJanitor: janitor} }
		e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()), WithMissionContext(provider))
		e.Complete(MeetAssistantQuestID)
		view := questView(e, ShowFolderQuestID)
		if view == nil || view.ActionURL != ShowFolderActionURL || view.ActionLabel != "Start" || view.InProgress {
			t.Fatalf("janitor %+v: card = %+v", janitor, view)
		}
	}
}

// The mission's grandfathering evidence (FR43): a completed Tidy your
// Downloads, a ready File Janitor, or a linked outside folder. A Tidy that was
// only skipped is no evidence.
func TestShowFolder_Satisfied(t *testing.T) {
	cases := []struct {
		name string
		snap Snapshot
		want bool
	}{
		{"fresh", Snapshot{}, false},
		{"legacy tidy completed", Snapshot{LegacyTidyCompleted: true}, true},
		{"file janitor ready", Snapshot{FileJanitorReady: true}, true},
		{"linked project folder", Snapshot{LinkedProjectWorkspaces: 1}, true},
		{"project workspaces alone", Snapshot{ProjectWorkspaces: 3}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
			if err := e.Backfill(ScannerFunc(func() Snapshot { return tc.snap })); err != nil {
				t.Fatal(err)
			}
			if completed(e, ShowFolderQuestID) != tc.want {
				t.Fatalf("completed = %t, want %t", completed(e, ShowFolderQuestID), tc.want)
			}
		})
	}
}

// A workspace the user set up from the assistant's folder offer completes the
// mission live (FR42); an ordinary create, or one that only claims the label
// without an offer, does not.
func TestIsFolderDigestWorkspaceCreated(t *testing.T) {
	created := func(data map[string]any) ws.Event {
		return ws.Event{Type: ws.EventWorkspaceCreated, WorkspaceID: "w", Data: data}
	}
	cases := []struct {
		name string
		ev   ws.Event
		want bool
	}{
		{"from the folder offer", created(map[string]any{"template_id": "writing-project", "entry_point": "folder_digest"}), true},
		{"blank workspace from the offer", created(map[string]any{"template_id": "", "kind": "workspace", "entry_point": "folder_digest"}), true},
		{"ordinary create", created(map[string]any{"template_id": "", "kind": "workspace"}), false},
		{"another entry point", created(map[string]any{"template_id": "", "entry_point": "map"}), false},
		{"no data", created(nil), false},
		{"not a create", ws.Event{Type: ws.EventWorkspaceUpdated, Data: map[string]any{"entry_point": "folder_digest"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFolderDigestWorkspaceCreated(tc.ev); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}

	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	e.HandleEvent(created(map[string]any{"template_id": "writing-project", "kind": "workspace", "entry_point": "folder_digest"}))
	if !completed(e, ShowFolderQuestID) {
		t.Fatal("a workspace created from the folder offer did not complete the mission")
	}
	// It is a project workspace too, so Connect one source's project branch
	// completes alongside, as any project create does.
	if !completed(e, ConnectSourceQuestID) {
		t.Fatal("the created project workspace did not count for Connect one source")
	}
}

func TestReconcileOnce_GrandfathersNewQuestsSilentlyOnce(t *testing.T) {
	store := &fakeStore{}
	store.state.BackfilledAt = time.Now().Add(-30 * 24 * time.Hour) // an established install
	fires := 0
	e := New(store, WithGraph(PersonalAssistantGraph()), WithOnComplete(func(Quest) { fires++ }))

	scans := 0
	snap := Snapshot{FileJanitorReady: true, HasBriefRevision: true, Workspaces: 4}
	scanner := ScannerFunc(func() Snapshot { scans++; return snap })

	marked, err := e.ReconcileOnce("starter-missions-v1", scanner, ShowFolderQuestID, ConnectSourceQuestID, FirstBriefQuestID)
	if err != nil || marked != 2 {
		t.Fatalf("marked=%d err=%v, want 2 (folder and brief)", marked, err)
	}
	if !completed(e, ShowFolderQuestID) || !completed(e, FirstBriefQuestID) || completed(e, ConnectSourceQuestID) {
		t.Fatalf("reconcile outcome wrong: %+v", e.Status().Missions)
	}
	// Only the named quests are reconciled: first contact stays for the live path.
	if completed(e, "t1-first-message") {
		t.Fatal("a quest outside the pass was grandfathered")
	}
	if fires != 0 {
		t.Fatalf("reconcile fired onComplete %d times; past work must not pay", fires)
	}

	// The pass never runs again, even when more evidence appears.
	snap.EmailOpsReady = true
	if marked, err := e.ReconcileOnce("starter-missions-v1", scanner, ConnectSourceQuestID); err != nil || marked != 0 {
		t.Fatalf("second pass marked %d err=%v", marked, err)
	}
	if scans != 1 || completed(e, ConnectSourceQuestID) {
		t.Fatalf("second pass scanned (%d scans) or completed Mission 04", scans)
	}

	// It survives a restart and a reset.
	if err := New(store, WithGraph(PersonalAssistantGraph())).Reset(); err != nil {
		t.Fatal(err)
	}
	after := New(store, WithGraph(PersonalAssistantGraph()))
	if marked, _ := after.ReconcileOnce("starter-missions-v1", scanner, ConnectSourceQuestID); marked != 0 || scans != 1 {
		t.Fatalf("a reset re-ran the pass: marked=%d scans=%d", marked, scans)
	}
}

func TestReconcileOnce_FreshInstallDefersToBackfill(t *testing.T) {
	store := &fakeStore{}
	e := New(store, WithGraph(PersonalAssistantGraph()))
	scans := 0
	scanner := ScannerFunc(func() Snapshot { scans++; return Snapshot{FileJanitorReady: true} })

	if marked, err := e.ReconcileOnce("starter-missions-v1", scanner, ShowFolderQuestID); err != nil || marked != 0 {
		t.Fatalf("marked=%d err=%v on a fresh install", marked, err)
	}
	if scans != 0 {
		t.Fatal("a fresh install scanned twice; Backfill covers it")
	}
	if _, recorded := store.state.Reconciled["starter-missions-v1"]; !recorded {
		t.Fatal("the pass was not recorded, so it would run after the first Backfill")
	}
	if err := e.Backfill(scanner); err != nil {
		t.Fatal(err)
	}
	if !completed(e, ShowFolderQuestID) || scans != 1 {
		t.Fatalf("Backfill did not grandfather Mission 03 (scans=%d)", scans)
	}
}

func TestResolveFirstBrief_AsksForAModelOnlyWhenNoneIsConfigured(t *testing.T) {
	if got := resolveFirstBrief(MissionContext{ModelConfigured: true}); got != (MissionPresentation{}) {
		t.Fatalf("with a model the copy changed: %+v", got)
	}
	got := resolveFirstBrief(MissionContext{})
	if got.Hint != "Add a model in Settings to generate one." || got.Why != "" || got.ActionURL != "" || got.InProgress {
		t.Fatalf("without a model = %+v", got)
	}

	// With a Personal HQ the button opens the Daily Brief station; a blank link
	// from the server keeps the fallback.
	station := "/workspaces/my-hq?station=daily-brief"
	if got := resolveFirstBrief(MissionContext{ModelConfigured: true, DailyBriefURL: station}); got != (MissionPresentation{ActionURL: station}) {
		t.Fatalf("with an HQ = %+v", got)
	}
	if got := resolveFirstBrief(MissionContext{ModelConfigured: true, DailyBriefURL: "  "}); got.ActionURL != "" {
		t.Fatalf("a blank station link was kept: %+v", got)
	}

	// Through the engine: the hint follows the why line while Mission 05 is
	// open, and disappears once it is done, where the advice no longer applies.
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()),
		WithMissionContext(func() MissionContext { return MissionContext{} }))
	brief := func() QuestView {
		for _, m := range e.Status().Missions {
			if m.ID == FirstBriefQuestID {
				return m
			}
		}
		t.Fatal("Mission 05 missing")
		return QuestView{}
	}
	// Before the hire the mission is locked: advice about acting on it waits.
	if why := brief().Why; why != firstBriefWhy {
		t.Fatalf("locked Mission 05 why = %q", why)
	}
	e.Complete(MeetAssistantQuestID)
	if why := brief().Why; why != firstBriefWhy+" Add a model in Settings to generate one." {
		t.Fatalf("open Mission 05 why = %q", why)
	}
	// No HQ: the button reads Open Daily Brief and falls back to the drawer.
	if card := brief(); card.ActionLabel != "Open Daily Brief" || card.ActionURL != FirstBriefFallbackURL {
		t.Fatalf("Mission 05 with no HQ: label=%q url=%q", card.ActionLabel, card.ActionURL)
	}
	e.Complete(FirstBriefQuestID)
	if why := brief().Why; why != firstBriefWhy {
		t.Fatalf("completed Mission 05 still advises: %q", why)
	}

	// With an HQ the same card opens the station.
	withHQ := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()),
		WithMissionContext(func() MissionContext { return MissionContext{DailyBriefURL: station} }))
	for _, m := range withHQ.Status().Missions {
		if m.ID == FirstBriefQuestID && (m.ActionLabel != "Open Daily Brief" || m.ActionURL != station) {
			t.Fatalf("Mission 05 with an HQ: label=%q url=%q", m.ActionLabel, m.ActionURL)
		}
	}
}

func TestChooseConnectSourceBranch_PriorityAndFallbacks(t *testing.T) {
	cases := []struct {
		focus []string
		want  ConnectSourceBranch
	}{
		{nil, BranchPlan},
		{[]string{}, BranchPlan},
		{[]string{"plan_my_day"}, BranchPlan},
		{[]string{"track_commitments_and_follow_ups"}, BranchPlan},
		{[]string{"something_else"}, BranchPlan},
		{[]string{"a_music_domain_focus"}, BranchPlan},
		{[]string{"keep_projects_moving"}, BranchProject},
		{[]string{"prepare_for_meetings"}, BranchCalendar},
		{[]string{"help_with_email"}, BranchEmail},
		// Priority: email, then calendar, then project, then plan.
		{[]string{"plan_my_day", "keep_projects_moving", "prepare_for_meetings", "help_with_email"}, BranchEmail},
		{[]string{"keep_projects_moving", "prepare_for_meetings"}, BranchCalendar},
		{[]string{"plan_my_day", "keep_projects_moving"}, BranchProject},
		{[]string{" help_with_email "}, BranchEmail},
	}
	for _, tc := range cases {
		if got := ChooseConnectSourceBranch(tc.focus); got != tc.want {
			t.Errorf("ChooseConnectSourceBranch(%v) = %s, want %s", tc.focus, got, tc.want)
		}
	}
}

func TestResolveConnectSource_PresentsTheChosenBranch(t *testing.T) {
	emailURL := "/?setup=quest&source=host&quest=email_ops_setup"
	cases := []struct {
		name string
		ctx  MissionContext
		want MissionPresentation
	}{
		{"plan keeps the static copy", MissionContext{FocusAreas: []string{"plan_my_day"}}, MissionPresentation{}},
		{
			"email starts the guided setup",
			MissionContext{FocusAreas: []string{"help_with_email"}, EmailQuestURL: emailURL},
			MissionPresentation{Title: "Set up email", Why: "So your brief can show what is waiting on you.", ActionURL: emailURL, ActionLabel: "Start"},
		},
		{
			"email in progress resumes it",
			MissionContext{FocusAreas: []string{"help_with_email"}, EmailQuestURL: emailURL, EmailQuestStarted: true},
			MissionPresentation{Title: "Set up email", Why: "So your brief can show what is waiting on you.", ActionURL: emailURL, ActionLabel: "Resume", InProgress: true},
		},
		{
			"email without a wired setup falls back to the plan",
			MissionContext{FocusAreas: []string{"help_with_email"}},
			MissionPresentation{},
		},
		{
			"calendar opens the creator on Calendar Ops",
			MissionContext{FocusAreas: []string{"prepare_for_meetings"}, EmailQuestStarted: true},
			MissionPresentation{Title: "Connect your calendar", Why: "So your brief can prepare you for today's meetings.", ActionURL: CalendarOpsCreateURL, ActionLabel: "Start"},
		},
		{
			// Starting a project is what Show your assistant a folder does now,
			// so the project focus keeps the plan's card (FR41).
			"project keeps the static copy",
			MissionContext{FocusAreas: []string{"keep_projects_moving"}},
			MissionPresentation{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveConnectSource(tc.ctx); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestIsProjectWorkspaceCreated(t *testing.T) {
	created := func(data map[string]any) ws.Event {
		return ws.Event{Type: ws.EventWorkspaceCreated, WorkspaceID: "w", Data: data}
	}
	cases := []struct {
		name string
		ev   ws.Event
		want bool
	}{
		{"blank workspace", created(map[string]any{"template_id": "", "kind": "workspace"}), true},
		{"other blueprint", created(map[string]any{"template_id": "content-production"}), true},
		{"Personal HQ", created(map[string]any{"template_id": "personal-ops"}), false},
		{"File Janitor", created(map[string]any{"template_id": "file-janitor"}), false},
		{"retired Downloads Janitor", created(map[string]any{"template_id": "downloads-janitor"}), false},
		{"Email Ops", created(map[string]any{"template_id": "email-ops"}), false},
		{"Calendar Ops", created(map[string]any{"template_id": "calendar-ops"}), false},
		{"a group", created(map[string]any{"template_id": "", "kind": "group"}), false},
		{"another producer without template_id", created(map[string]any{"name": "Orchestration"}), false},
		{"no data", created(nil), false},
		{"not a create", ws.Event{Type: ws.EventWorkspaceUpdated, Data: map[string]any{"template_id": ""}}, false},
	}
	for _, tc := range cases {
		if got := IsProjectWorkspaceCreated(tc.ev); got != tc.want {
			t.Errorf("%s: got %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestConnectSource_ProjectEventCompletesOnce(t *testing.T) {
	fires := 0
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()), WithOnComplete(func(q Quest) {
		if q.ID == ConnectSourceQuestID {
			fires++
		}
	}))
	e.HandleEvent(ws.Event{Type: ws.EventWorkspaceCreated, Data: map[string]any{"template_id": "file-janitor"}})
	if completed(e, ConnectSourceQuestID) {
		t.Fatal("a starter blueprint completed Connect one source")
	}
	project := ws.Event{Type: ws.EventWorkspaceCreated, Data: map[string]any{"template_id": "", "kind": "workspace"}}
	e.HandleEvent(project)
	e.HandleEvent(project)
	if !completed(e, ConnectSourceQuestID) || fires != 1 {
		t.Fatalf("completed=%t fires=%d, want completed once", completed(e, ConnectSourceQuestID), fires)
	}
}

func TestConnectSource_BackfillFromAnyBranch(t *testing.T) {
	for name, snap := range map[string]Snapshot{
		"email ready":       {EmailOpsReady: true},
		"calendar ready":    {CalendarReady: true},
		"project workspace": {ProjectWorkspaces: 1},
		"first assignment":  {FirstAssignmentCompleted: true},
		"legacy first day":  {LegacyFirstDayCompleted: true},
	} {
		t.Run(name, func(t *testing.T) {
			e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
			if err := e.Backfill(ScannerFunc(func() Snapshot { return snap })); err != nil {
				t.Fatal(err)
			}
			if !completed(e, ConnectSourceQuestID) {
				t.Fatalf("%s did not grandfather Connect one source", name)
			}
		})
	}
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	if err := e.Backfill(ScannerFunc(func() Snapshot {
		return Snapshot{Workspaces: 3, HasPersonalHQ: true, FileJanitorReady: true}
	})); err != nil {
		t.Fatal(err)
	}
	if completed(e, ConnectSourceQuestID) {
		t.Fatal("HQ and File Janitor alone grandfathered Connect one source")
	}
}

func TestHasCompleted_SeesRetiredQuestIDs(t *testing.T) {
	store := &fakeStore{}
	store.state.CompletedQuests = map[string]time.Time{PersonalAssistantFirstDayQuestID: time.Now()}
	e := New(store, WithGraph(PersonalAssistantGraph()))
	if !e.HasCompleted(PersonalAssistantFirstDayQuestID) {
		t.Fatal("a persisted completion of a retired quest must stay visible")
	}
	if e.HasCompleted(ConnectSourceQuestID) {
		t.Fatal("Connect one source reported complete without evidence")
	}
	// A retired ID cannot be completed again through the graph.
	if e.Complete(PersonalAssistantFirstDayQuestID) {
		t.Fatal("Complete accepted a quest the graph no longer contains")
	}
}

func TestPersonalAssistantGraph_BackfillEvidence(t *testing.T) {
	cases := []struct {
		name string
		snap Snapshot
		want []string
	}{
		{"fresh", Snapshot{}, nil},
		{"assistant hired", Snapshot{AssistantHired: true}, []string{MeetAssistantQuestID}},
		{"janitor ready", Snapshot{FileJanitorReady: true}, []string{ShowFolderQuestID}},
		{"legacy tidy completed", Snapshot{LegacyTidyCompleted: true}, []string{ShowFolderQuestID}},
		{"linked project folder", Snapshot{LinkedProjectWorkspaces: 1}, []string{ShowFolderQuestID}},
		{"first look finished", Snapshot{FolderFirstTaskFinished: true}, []string{FolderFirstLookQuestID}},
		{"first assignment", Snapshot{FirstAssignmentCompleted: true}, []string{ConnectSourceQuestID}},
		{"legacy first day", Snapshot{LegacyFirstDayCompleted: true}, []string{ConnectSourceQuestID}},
		{"brief revision", Snapshot{HasBriefRevision: true}, []string{FirstBriefQuestID}},
	}
	missions := []string{MeetAssistantQuestID, ShowFolderQuestID, FolderFirstLookQuestID, ConnectSourceQuestID, FirstBriefQuestID}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
			if err := e.Backfill(ScannerFunc(func() Snapshot { return tc.snap })); err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{}
			for _, id := range tc.want {
				want[id] = true
			}
			for _, id := range missions {
				if completed(e, id) != want[id] {
					t.Fatalf("%s completed = %t, want %t", id, completed(e, id), want[id])
				}
			}
		})
	}
}

// missionLocks renders each featured mission as "id:locked:reason" in Order.
func missionLocks(e *Engine) []string {
	var got []string
	for _, m := range e.Status().Missions {
		got = append(got, fmt.Sprintf("%s:%t:%s", m.ID, m.Locked, m.LockedReason))
	}
	return got
}

func TestPersonalAssistantGraph_LaterMissionsLockUntilTheHire(t *testing.T) {
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))

	st := e.Status()
	if len(st.Missions) != 5 || st.Missions[0].ID != MeetAssistantQuestID {
		t.Fatalf("missions = %v, want five with Meet your assistant first", missionLocks(e))
	}
	wantLocked := []string{
		MeetAssistantQuestID + ":false:",
		ShowFolderQuestID + ":true:Build your HQ first",
		FolderFirstLookQuestID + ":true:Show your assistant a folder first",
		ConnectSourceQuestID + ":true:Meet your assistant first",
		FirstBriefQuestID + ":true:Meet your assistant first",
	}
	if got := missionLocks(e); strings.Join(got, " ") != strings.Join(wantLocked, " ") {
		t.Fatalf("before the hire = %v, want %v", got, wantLocked)
	}
	// The tier list carries the same lock the card reads.
	if view := questView(e, ShowFolderQuestID); view == nil || !view.Locked {
		t.Fatalf("tier view not locked: %+v", view)
	}
	// The next quest is the one the user can act on, never a locked one.
	if st.NextQuest == nil || st.NextQuest.ID != MeetAssistantQuestID || st.NextQuest.Locked {
		t.Fatalf("next quest = %+v, want Meet your assistant", st.NextQuest)
	}

	if !e.Complete(MeetAssistantQuestID) {
		t.Fatal("Complete(Meet your assistant) was not newly recorded")
	}
	// The hire opens every mission that waited on it. The folder missions wait
	// on the HQ and then on the folder, so they stay locked.
	for _, m := range e.Status().Missions {
		wantLock := m.ID == ShowFolderQuestID || m.ID == FolderFirstLookQuestID
		if m.Locked != wantLock {
			t.Fatalf("mission %s locked = %t after the hire: %+v", m.ID, m.Locked, m)
		}
	}
	if next := e.Status().NextQuest; next == nil || next.ID != ConnectSourceQuestID {
		t.Fatalf("next quest after the hire = %+v, want the first unlocked mission", next)
	}
}

// Show your assistant a folder waits on a real HQ (FR21, FR22). Not now on the
// HQ card skips the retired HQ quest and so keeps the lock; building opens it;
// and the lock names its one fix.
func TestShowFolder_LockedUntilTheHQIsBuilt(t *testing.T) {
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	e.Complete(MeetAssistantQuestID)

	view := questView(e, ShowFolderQuestID)
	if view == nil || !view.Locked || view.LockedReason != "Build your HQ first" {
		t.Fatalf("before the HQ: %+v", view)
	}
	if view.LockedAction == nil || view.LockedAction.Kind != "hq_card" || view.LockedAction.Label != "Build My HQ" {
		t.Fatalf("the lock names no fix: %+v", view.LockedAction)
	}

	// Not now skips the HQ quest. Only a completion unlocks.
	if err := e.Skip(BuildHQQuestID); err != nil {
		t.Fatal(err)
	}
	if view := questView(e, ShowFolderQuestID); view == nil || !view.Locked || view.LockedAction == nil {
		t.Fatalf("a deferred HQ opened the folder mission: %+v", view)
	}

	e.Complete(BuildHQQuestID)
	view = questView(e, ShowFolderQuestID)
	if view == nil || view.Locked || view.LockedReason != "" || view.LockedAction != nil {
		t.Fatalf("a built HQ did not open the folder mission: %+v", view)
	}

	// The lock is gone once the mission resolves, and a quest with no override
	// keeps the plain sentence.
	if other := questView(e, ConnectSourceQuestID); other == nil || other.LockedAction != nil {
		t.Fatalf("an unrelated mission carries a lock action: %+v", other)
	}
	plain := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	if other := questView(plain, ConnectSourceQuestID); other == nil || other.LockedReason != "Meet your assistant first" {
		t.Fatalf("the default lock sentence changed: %+v", other)
	}
}

// See what your assistant found waits on a folder actually being shown (FR8).
// Skipping Show a folder is not showing one, so the lock stays; a real
// completion opens it.
func TestFolderFirstLook_LockedUntilAFolderIsShown(t *testing.T) {
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	e.Complete(MeetAssistantQuestID)

	view := questView(e, FolderFirstLookQuestID)
	if view == nil || !view.Locked || view.LockedReason != "Show your assistant a folder first" {
		t.Fatalf("before a folder is shown: %+v", view)
	}
	// A locked mission carries no advice about acting on it.
	if view.Why != folderFirstLookWhy || view.InProgress {
		t.Fatalf("locked card copy = %q in progress %t", view.Why, view.InProgress)
	}

	if err := e.Skip(ShowFolderQuestID); err != nil {
		t.Fatal(err)
	}
	if view := questView(e, FolderFirstLookQuestID); view == nil || !view.Locked {
		t.Fatalf("a skipped Show a folder opened the first look: %+v", view)
	}

	e.Complete(ShowFolderQuestID)
	if view := questView(e, FolderFirstLookQuestID); view == nil || view.Locked || view.LockedReason != "" {
		t.Fatalf("showing a folder did not open the first look: %+v", view)
	}
	// Work done early still counts: the lock is presentation only.
	early := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	if !early.Complete(FolderFirstLookQuestID) || !completed(early, FolderFirstLookQuestID) {
		t.Fatal("a locked first look refused a real completion")
	}
}

// The card's copy and action for every state of the first look (FR10).
func TestResolveFolderFirstLook_EveryState(t *testing.T) {
	workspace := MissionFirstTask{
		WorkspaceName: "Thesis", WorkspaceRoute: "/workspaces/thesis", FolderName: "thesis-draft",
		TicketRoute: "/workspaces/thesis?ticket=t-1",
	}
	with := func(change func(*MissionFirstTask)) MissionFirstTask {
		task := workspace
		change(&task)
		return task
	}
	const hintStart = folderFirstLookWhy + " Spends model tokens for one read-only look at the folder."
	const hintNone = folderFirstLookWhy + " Show it a project folder to start."
	cases := []struct {
		name       string
		task       MissionFirstTask
		url, label string
		why        string
		inProgress bool
	}{
		{"no folder workspace", MissionFirstTask{}, ShowFolderActionURL, "Show a folder", hintNone, false},
		{"none state", with(func(m *MissionFirstTask) { m.State = MissionFirstTaskNone }),
			ShowFolderActionURL, "Show a folder", hintNone, false},
		{"seeded", with(func(m *MissionFirstTask) { m.State, m.CanStart = MissionFirstTaskSeeded, true }),
			FolderFirstLookActionURL, "Start first look", hintStart, false},
		{"cannot start: fix is in the workspace", with(func(m *MissionFirstTask) {
			m.State, m.Blocked = MissionFirstTaskSeeded, "Open Thesis to finish its setup first."
		}), "/workspaces/thesis", "Open Thesis", "Open Thesis to finish its setup first.", false},
		{"cannot start: fix is elsewhere", with(func(m *MissionFirstTask) {
			m.State, m.Blocked = MissionFirstTaskSeeded, "Add a model in Settings to run the first look."
			m.BlockedURL, m.BlockedLabel = "/settings#system-model", "Open Settings"
		}), "/settings#system-model", "Open Settings", "Add a model in Settings to run the first look.", false},
		{"running", with(func(m *MissionFirstTask) { m.State = MissionFirstTaskRunning }),
			"/workspaces/thesis", "Open Thesis", "Working on thesis-draft…", true},
		{"running without a folder name", with(func(m *MissionFirstTask) {
			m.State, m.FolderName = MissionFirstTaskRunning, ""
		}), "/workspaces/thesis", "Open Thesis", "Working on Thesis…", true},
		{"paused to ask the user", with(func(m *MissionFirstTask) {
			m.State, m.Blocked = MissionFirstTaskWaiting, "The first look is waiting for your answer. Open Thesis to continue."
		}), "/workspaces/thesis", "Open Thesis", "The first look is waiting for your answer. Open Thesis to continue.", false},
		{"failed", with(func(m *MissionFirstTask) { m.State = MissionFirstTaskFailed }),
			FolderFirstLookActionURL, "Try again", "The first look did not finish. Try again.", false},
		{"finished while the mission is open", with(func(m *MissionFirstTask) { m.State = MissionFirstTaskFinished }),
			ShowFolderActionURL, "Show another folder",
			"Your assistant already looked at Thesis. Show it another folder to see what it finds there.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := func() MissionContext { return MissionContext{FolderFirstTask: tc.task} }
			e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()), WithMissionContext(provider))
			e.Complete(MeetAssistantQuestID)
			e.Complete(ShowFolderQuestID)
			view := questView(e, FolderFirstLookQuestID)
			if view == nil {
				t.Fatal("mission missing")
			}
			if view.ActionURL != tc.url || view.ActionLabel != tc.label {
				t.Fatalf("action = %q %q, want %q %q", view.ActionLabel, view.ActionURL, tc.label, tc.url)
			}
			if view.Why != tc.why || view.InProgress != tc.inProgress {
				t.Fatalf("why = %q in progress %t, want %q %t", view.Why, view.InProgress, tc.why, tc.inProgress)
			}
			if view.Title != "See what your assistant found" || view.Locked {
				t.Fatalf("title %q locked %t", view.Title, view.Locked)
			}
		})
	}

	// Once the look has paid, the card rests on the static copy: no hint, no
	// "in progress", whatever the task is doing now.
	provider := func() MissionContext {
		return MissionContext{FolderFirstTask: with(func(m *MissionFirstTask) { m.State = MissionFirstTaskRunning })}
	}
	done := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()), WithMissionContext(provider))
	done.Complete(FolderFirstLookQuestID)
	if view := questView(done, FolderFirstLookQuestID); view == nil || view.Status != StatusCompleted || view.InProgress {
		t.Fatalf("completed mission = %+v", view)
	}
}

// An install that already ran a first look before the mission existed sees it
// done after the upgrade: once, silently, so no Craft is paid for past work
// (FR12). A fresh install records the pass and leaves the mission to the live
// hook.
func TestReconcileOnce_GrandfathersAFinishedFirstLook(t *testing.T) {
	store := &fakeStore{}
	store.state.BackfilledAt = time.Now().Add(-30 * 24 * time.Hour)
	fires := 0
	e := New(store, WithGraph(PersonalAssistantGraph()), WithOnComplete(func(Quest) { fires++ }))
	scanner := ScannerFunc(func() Snapshot { return Snapshot{FolderFirstTaskFinished: true} })

	marked, err := e.ReconcileOnce("folder-first-look-v1", scanner, FolderFirstLookQuestID)
	if err != nil || marked != 1 || !completed(e, FolderFirstLookQuestID) {
		t.Fatalf("marked=%d err=%v completed=%t", marked, err, completed(e, FolderFirstLookQuestID))
	}
	if fires != 0 {
		t.Fatalf("reconcile fired onComplete %d times; past work must not pay", fires)
	}

	unfinished := &fakeStore{}
	unfinished.state.BackfilledAt = time.Now().Add(-30 * 24 * time.Hour)
	open := New(unfinished, WithGraph(PersonalAssistantGraph()))
	if marked, err := open.ReconcileOnce("folder-first-look-v1", ScannerFunc(func() Snapshot { return Snapshot{} }), FolderFirstLookQuestID); err != nil || marked != 0 {
		t.Fatalf("no finished look: marked=%d err=%v", marked, err)
	}
	if completed(open, FolderFirstLookQuestID) {
		t.Fatal("the mission completed without a finished first look")
	}
}

// Locking is presentation only (FR31): work done early still counts, and a
// resolved mission is never shown as locked.
func TestPersonalAssistantGraph_LockedMissionsStillComplete(t *testing.T) {
	fires := []string{}
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()),
		WithOnComplete(func(q Quest) { fires = append(fires, q.ID) }))

	if !e.Complete(BuildHQQuestID) {
		t.Fatal("a locked quest refused a direct Complete")
	}
	e.HandleEvent(ws.Event{Type: ws.EventWorkspaceCreated, Data: map[string]any{"template_id": "", "kind": "workspace"}})
	if !e.HasCompleted(BuildHQQuestID) || !completed(e, ConnectSourceQuestID) {
		t.Fatalf("locked quests did not record completion: %v", missionLocks(e))
	}
	if strings.Join(fires, ",") != BuildHQQuestID+","+ConnectSourceQuestID {
		t.Fatalf("live completions fired %v", fires)
	}
	if err := e.Skip(FirstBriefQuestID); err != nil {
		t.Fatal(err)
	}
	for _, m := range e.Status().Missions {
		resolved := m.Status == StatusCompleted || m.Status == StatusSkipped
		if resolved && m.Locked {
			t.Fatalf("resolved mission %s is shown locked", m.ID)
		}
	}
	// The HQ was built above, so Show a folder is open; the first look still
	// waits on the folder.
	if view := questView(e, FolderFirstLookQuestID); view == nil || !view.Locked {
		t.Fatalf("the still-open mission lost its lock: %+v", view)
	}

	// Backfill runs for locked quests too.
	b := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	if err := b.Backfill(ScannerFunc(func() Snapshot { return Snapshot{FileJanitorReady: true} })); err != nil {
		t.Fatal(err)
	}
	if !completed(b, ShowFolderQuestID) {
		t.Fatal("backfill skipped a locked quest")
	}
}

// The one required mission keeps the Starter tier current even when every
// optional mission is resolved, and it cannot be skipped around.
func TestMeetAssistant_IsRequiredAndHoldsTheStarterTier(t *testing.T) {
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	if err := e.Skip(MeetAssistantQuestID); !errors.Is(err, ErrQuestNotOptional) {
		t.Fatalf("Skip(Meet your assistant) = %v, want ErrQuestNotOptional", err)
	}
	for _, id := range []string{BuildHQQuestID, ShowFolderQuestID, FolderFirstLookQuestID, ConnectSourceQuestID, FirstBriefQuestID} {
		if err := e.Skip(id); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.Status().CurrentTier; got != 1 {
		t.Fatalf("current tier = %d, want 1 until the assistant is hired", got)
	}
	e.Complete(MeetAssistantQuestID)
	if st := e.Status(); st.CurrentTier != 1 || !st.AllComplete || st.TotalCount != 5 {
		t.Fatalf("status after the hire = %+v", st)
	}
}

func TestLockedUntil_UnknownGateNeverLocks(t *testing.T) {
	graph := Graph{
		Quests: []Quest{
			{ID: "a", Tier: 1, Title: "A", Featured: true, Order: 1},
			{ID: "b", Tier: 1, Title: "B", Featured: true, Order: 2, LockedUntil: "missing"},
			{ID: "c", Tier: 1, Title: "C", Featured: true, Order: 3, LockedUntil: "a"},
		},
		TierNames: map[int]string{1: "One"}, TotalTiers: 1,
	}
	e := New(&fakeStore{}, WithGraph(graph))
	want := "a:false: b:false: c:true:A first"
	if got := strings.Join(missionLocks(e), " "); got != want {
		t.Fatalf("locks = %s, want %s", got, want)
	}
}
