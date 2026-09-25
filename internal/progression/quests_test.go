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
		BuildHQQuestID:       1, ShowFolderQuestID: 1, ConnectSourceQuestID: 1, FirstBriefQuestID: 1,
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

	// The five starter missions, in order, all featured. Meet your assistant is
	// the one required mission and gates the other four; those stay optional so
	// no mission after the hire ever locks the Daily loop tier.
	wantMissions := []struct {
		id, title, url, label string
		optional              bool
		lockedUntil           string
	}{
		{MeetAssistantQuestID, "Meet your assistant", MeetAssistantActionURL, "Start", false, ""},
		{BuildHQQuestID, "Build My HQ", GuidedBuildHQActionURL, "Build My HQ", true, MeetAssistantQuestID},
		{ShowFolderQuestID, "Show your assistant a folder", ShowFolderActionURL, "Start", true, MeetAssistantQuestID},
		{ConnectSourceQuestID, "Plan my first day", PlanFirstDayActionURL, "Start", true, MeetAssistantQuestID},
		{FirstBriefQuestID, "Read your first Daily Brief", "/", "Open Today", true, MeetAssistantQuestID},
	}
	for i, want := range wantMissions {
		q := graph.Quests[i]
		if q.ID != want.id || q.Order != i+1 || !q.Featured || q.Optional != want.optional {
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
	for _, q := range graph.Quests[len(wantMissions):] {
		if q.Featured || q.Order != 0 || q.LockedUntil != "" {
			t.Fatalf("non-mission quest %s is featured (order %d) or locked (%q)", q.ID, q.Order, q.LockedUntil)
		}
	}

	names := []string{"Starter", "Daily loop", "Recruit", "Equip", "Automate", "Command"}
	for i, name := range names {
		if graph.TierNames[i+1] != name {
			t.Fatalf("cohort tier %d = %q, want %q", i+1, graph.TierNames[i+1], name)
		}
	}
	if graph.TotalTiers != 6 {
		t.Fatalf("cohort total tiers = %d", graph.TotalTiers)
	}
	// Renaming the cohort's tiers must not leak into the built-in names.
	if TierName(1) != "First Contact" {
		t.Fatalf("built-in tier 1 renamed to %q", TierName(1))
	}
}

func TestStatus_UsesTheGraphsTierNames(t *testing.T) {
	e := New(&fakeStore{}, WithGraph(PersonalAssistantGraph()))
	st := e.Status()
	if st.Tiers[0].Name != "Starter" || st.Tiers[1].Name != "Daily loop" {
		t.Fatalf("tier names = %q, %q", st.Tiers[0].Name, st.Tiers[1].Name)
	}
	if st.TotalTiers != 6 || st.CurrentTier != 1 {
		t.Fatalf("total/current = %d/%d", st.TotalTiers, st.CurrentTier)
	}

	// A hired user who built HQ and resolved every mission sits in Daily loop,
	// never back in a first-contact tier.
	for _, id := range []string{MeetAssistantQuestID, BuildHQQuestID, ShowFolderQuestID, ConnectSourceQuestID} {
		e.Complete(id)
	}
	if err := e.Skip(FirstBriefQuestID); err != nil {
		t.Fatal(err)
	}
	if got := e.Status().CurrentTier; got != 2 {
		t.Fatalf("current tier after the starter missions = %d, want 2", got)
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
	if why := brief().Why; why != firstBriefWhy+" Add a model in Settings to generate one." {
		t.Fatalf("open Mission 05 why = %q", why)
	}
	e.Complete(FirstBriefQuestID)
	if why := brief().Why; why != firstBriefWhy {
		t.Fatalf("completed Mission 05 still advises: %q", why)
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
		{"first assignment", Snapshot{FirstAssignmentCompleted: true}, []string{ConnectSourceQuestID}},
		{"legacy first day", Snapshot{LegacyFirstDayCompleted: true}, []string{ConnectSourceQuestID}},
		{"brief revision", Snapshot{HasBriefRevision: true}, []string{FirstBriefQuestID}},
	}
	missions := []string{MeetAssistantQuestID, ShowFolderQuestID, ConnectSourceQuestID, FirstBriefQuestID}
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
		BuildHQQuestID + ":true:Meet your assistant first",
		ShowFolderQuestID + ":true:Meet your assistant first",
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
	for _, m := range e.Status().Missions {
		if m.Locked || m.LockedReason != "" {
			t.Fatalf("mission %s still locked after the hire: %+v", m.ID, m)
		}
	}
	if next := e.Status().NextQuest; next == nil || next.ID != BuildHQQuestID {
		t.Fatalf("next quest after the hire = %+v, want Build My HQ", next)
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
	if !completed(e, BuildHQQuestID) || !completed(e, ConnectSourceQuestID) {
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
	if view := questView(e, ShowFolderQuestID); view == nil || !view.Locked {
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
	for _, id := range []string{BuildHQQuestID, ShowFolderQuestID, ConnectSourceQuestID, FirstBriefQuestID} {
		if err := e.Skip(id); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.Status().CurrentTier; got != 1 {
		t.Fatalf("current tier = %d, want 1 until the assistant is hired", got)
	}
	e.Complete(MeetAssistantQuestID)
	if got := e.Status().CurrentTier; got != 2 {
		t.Fatalf("current tier after the hire = %d, want 2", got)
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
