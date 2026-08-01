package workspacecapability

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type stubRuntime struct {
	status Status
	err    error
	calls  int
}

func (s *stubRuntime) CapabilityStatus(string) (Status, error) {
	s.calls++
	return s.status, s.err
}

func fileJanitorRecord() workspace.InstalledCapability {
	return workspace.InstalledCapability{
		ID:          workspace.CapabilityFileJanitor,
		Version:     FileJanitorDefinitionVersion,
		InstalledAt: time.Now(),
		Source:      workspace.InstallSourceInPlace,
	}
}

func mustBuiltinRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	return r
}

func TestBuiltinRegistry_RegistersFileJanitorVersion1(t *testing.T) {
	r := mustBuiltinRegistry(t)

	def, ok := r.Definition(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatal("file-janitor is not in the compiled allowlist")
	}
	if def.Version != 1 {
		t.Fatalf("definition version = %d, want 1", def.Version)
	}
	if def.Display.Name != "File Janitor" {
		t.Fatalf("display name = %q", def.Display.Name)
	}
	if def.Requirements.MaxInstallsPerWorkspace != 1 {
		t.Fatalf("v1 allows at most one install per workspace, got %d", def.Requirements.MaxInstallsPerWorkspace)
	}
	if !def.Requirements.LocalFolderAccess {
		t.Fatal("File Janitor requires local folder access")
	}
	if def.Companion == nil || !def.Companion.ReadOnly {
		t.Fatalf("companion must be declared read-only: %+v", def.Companion)
	}

	// The catalog item must be able to state the safety model before install
	// (FR-18): one folder, metadata-first, approval required, fixed Filed/.
	highlights := strings.ToLower(strings.Join(def.Display.Highlights, " | "))
	for _, want := range []string{"one folder", "not file contents", "approval", "filed/"} {
		if !strings.Contains(highlights, want) {
			t.Errorf("catalog highlights missing %q: %v", want, def.Display.Highlights)
		}
	}
}

func TestBuiltinRegistry_RetainsLegacyDownloadsAliases(t *testing.T) {
	def, ok := mustBuiltinRegistry(t).Definition(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatal("file-janitor missing")
	}

	if !def.API.MatchesPrefix(FileJanitorAPIPrefix) {
		t.Error("canonical API prefix does not match itself")
	}
	if !def.API.MatchesPrefix(LegacyAPIPrefixDownloadsJanitor) {
		t.Error("legacy downloads-janitor route prefix must keep resolving (FR-133)")
	}
	if !def.Setup.MatchesAdapterID(FileJanitorSetupAdapterID) {
		t.Error("canonical setup adapter does not match itself")
	}
	if !def.Setup.MatchesAdapterID(LegacySetupAdapterDownloadsJanitor) {
		t.Error("legacy downloads_janitor setup adapter must keep resolving (FR-134)")
	}
	if !def.Setup.MatchesDirectoryRequirementKey(LegacyDirectoryRequirementKeyDownloadsRoot) {
		t.Error("legacy downloads-root requirement key must keep resolving (FR-134)")
	}
	if def.Setup.MatchesAdapterID("some_other_adapter") {
		t.Error("an unrelated adapter id must not resolve to File Janitor")
	}
	if def.API.MatchesPrefix("") {
		t.Error("an empty prefix must not match")
	}
}

func TestRegistry_RejectsMalformedAndDuplicateDefinitions(t *testing.T) {
	valid := FileJanitorDefinition()

	tests := []struct {
		name string
		defs []Definition
		want string
	}{
		{"empty id", []Definition{{Version: 1, Display: Display{Name: "X"}}}, "id is required"},
		{"zero version", []Definition{{ID: "x", Display: Display{Name: "X"}}}, "version must be positive"},
		{"no display name", []Definition{{ID: "x", Version: 1}}, "display name is required"},
		{"duplicate id", []Definition{valid, valid}, "registered twice"},
		{
			"default tab outside allowlist",
			[]Definition{{ID: "x", Version: 1, Display: Display{Name: "X"}, Console: ConsoleDescriptor{Tabs: []string{"review"}, DefaultTab: "nope"}}},
			"not in the tab allowlist",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegistry(tc.defs...)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// TestRegistry_ResolveFailsClosedForUnknownID is the FR-14 guarantee: a
// persisted ID may only select a compiled definition. An unknown one stays
// visible as metadata and activates nothing.
func TestRegistry_ResolveFailsClosedForUnknownID(t *testing.T) {
	r := mustBuiltinRegistry(t)

	record := workspace.InstalledCapability{
		ID:          "totally-made-up",
		Version:     3,
		InstalledAt: time.Now(),
		Source:      "hand-edited",
	}

	resolved := r.Resolve(record)
	if resolved.Available {
		t.Fatal("an unknown capability id resolved as available")
	}
	if resolved.Unavailable == "" {
		t.Fatal("unavailable records must explain why")
	}
	// Still visible: the user can see something is installed.
	if resolved.Record.ID != "totally-made-up" {
		t.Fatalf("the install record was not preserved: %+v", resolved.Record)
	}
	if resolved.Definition.ID != "totally-made-up" {
		t.Fatalf("placeholder definition lost the id: %+v", resolved.Definition)
	}
	// But it activates nothing: no routes, no console, no companion, no setup.
	if resolved.Definition.API.Prefix != "" || len(resolved.Definition.API.LegacyPrefixes) != 0 {
		t.Errorf("placeholder definition exposes routes: %+v", resolved.Definition.API)
	}
	if resolved.Definition.Console.PanelID != "" || len(resolved.Definition.Console.Tabs) != 0 {
		t.Errorf("placeholder definition exposes a console: %+v", resolved.Definition.Console)
	}
	if resolved.Definition.Setup.AdapterID != "" {
		t.Errorf("placeholder definition exposes a setup adapter: %+v", resolved.Definition.Setup)
	}
	if resolved.Definition.Companion != nil {
		t.Errorf("placeholder definition exposes a companion: %+v", resolved.Definition.Companion)
	}
	if resolved.Definition.Automation != (AutomationDescriptor{}) {
		t.Errorf("placeholder definition exposes automation: %+v", resolved.Definition.Automation)
	}
	if _, ok := r.Runtime("totally-made-up"); ok {
		t.Error("an unknown capability id resolved to a runtime")
	}
}

// TestRegistry_BindRuntimeRejectsUnknownID proves the registry cannot be
// extended through the runtime-binding door either: behavior may only be
// attached to an already-compiled definition.
func TestRegistry_BindRuntimeRejectsUnknownID(t *testing.T) {
	r := mustBuiltinRegistry(t)

	if err := r.BindRuntime("totally-made-up", &stubRuntime{}); err == nil {
		t.Fatal("expected binding a runtime to an unknown id to fail")
	}
	if err := r.BindRuntime(workspace.CapabilityFileJanitor, nil); err == nil {
		t.Fatal("expected binding a nil runtime to fail")
	}
	if r.Has("totally-made-up") {
		t.Fatal("a rejected binding added the id to the allowlist")
	}
}

// TestDefinition_CarriesNoExecutableReference is a structural guard for FR-14.
// If someone adds a field that could name a script, URL, command, path, or
// module specifier, a workspace-supplied install record would gain a way to
// point at it. Definitions must stay inert metadata.
func TestDefinition_CarriesNoExecutableReference(t *testing.T) {
	banned := []string{"url", "uri", "path", "command", "cmd", "script", "exec", "module", "plugin", "binary", "endpoint", "handler", "func", "hook"}

	var walk func(t *testing.T, typ reflect.Type, trail string)
	walk = func(t *testing.T, typ reflect.Type, trail string) {
		for typ.Kind() == reflect.Ptr || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := trail + "." + field.Name
			lower := strings.ToLower(field.Name)
			for _, bad := range banned {
				if strings.Contains(lower, bad) {
					t.Errorf("%s looks like an executable reference (%q); Definition must stay inert metadata (FR-14)", name, bad)
				}
			}
			switch field.Type.Kind() {
			case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Interface:
				t.Errorf("%s has kind %s; Definition must be serializable inert data, with behavior bound separately via Runtime", name, field.Type.Kind())
			}
			walk(t, field.Type, name)
		}
	}

	walk(t, reflect.TypeOf(Definition{}), "Definition")
}

func TestRegistry_ResolveReportsVersionDrift(t *testing.T) {
	r := mustBuiltinRegistry(t)

	current := r.Resolve(fileJanitorRecord())
	if !current.Available || current.NeedsMigration {
		t.Fatalf("a record at the current version must not need migration: %+v", current)
	}

	older := fileJanitorRecord()
	older.Version = FileJanitorDefinitionVersion - 1
	if older.Version < 1 {
		older.Version = 1
		// At definition version 1 there is no older version to drift from;
		// simulate the forward case instead.
		older.Version = FileJanitorDefinitionVersion + 1
	}
	drifted := r.Resolve(older)
	if !drifted.Available {
		t.Fatal("a version-drifted record must still resolve to its definition")
	}
	if !drifted.NeedsMigration {
		t.Fatalf("expected version drift to be reported: record=%d definition=%d", older.Version, FileJanitorDefinitionVersion)
	}
}

func TestRegistry_ResolveNormalizesID(t *testing.T) {
	r := mustBuiltinRegistry(t)

	record := fileJanitorRecord()
	record.ID = "  FILE-JANITOR  "

	resolved := r.Resolve(record)
	if !resolved.Available {
		t.Fatalf("a differently-cased id must resolve: %+v", resolved)
	}
	if resolved.Record.ID != workspace.CapabilityFileJanitor {
		t.Fatalf("record id not normalized: %q", resolved.Record.ID)
	}
}

func TestRegistry_ResolveEmptyIDIsUnavailable(t *testing.T) {
	resolved := mustBuiltinRegistry(t).Resolve(workspace.InstalledCapability{Version: 1})
	if resolved.Available {
		t.Fatal("an empty capability id resolved as available")
	}
	if !strings.Contains(resolved.Unavailable, "no capability id") {
		t.Fatalf("unexpected reason: %q", resolved.Unavailable)
	}
}

func TestRegistry_StatusUsesBoundRuntime(t *testing.T) {
	r := mustBuiltinRegistry(t)
	runtime := &stubRuntime{status: Status{State: StatusWatching, Detail: "Watching", Configured: true}}
	if err := r.BindRuntime(workspace.CapabilityFileJanitor, runtime); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}

	status, resolved, err := r.Status(fileJanitorRecord(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !resolved.Available {
		t.Fatal("expected the record to resolve")
	}
	if status.State != StatusWatching {
		t.Fatalf("state = %q, want watching", status.State)
	}
	if runtime.calls != 1 {
		t.Fatalf("runtime consulted %d times, want 1", runtime.calls)
	}
}

// TestRegistry_StatusIsUnavailableWithoutRuntime proves health is never taken
// from the persisted record (FR-6): an install with no wired runtime reports
// unavailable rather than inventing a healthy-looking state.
func TestRegistry_StatusIsUnavailableWithoutRuntime(t *testing.T) {
	r := mustBuiltinRegistry(t)

	status, resolved, err := r.Status(fileJanitorRecord(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !resolved.Available {
		t.Fatal("the definition should still resolve")
	}
	if status.State != StatusUnavailable {
		t.Fatalf("state = %q, want unavailable", status.State)
	}
	if status.Configured {
		t.Fatal("an unavailable capability must not claim to be configured")
	}
}

// TestRegistry_StatusSurvivesRuntimeFailure covers FR-145: a capability health
// failure is isolated to that capability. The caller gets a renderable
// needs-attention status alongside the error, so the workspace and Map keep
// loading.
func TestRegistry_StatusSurvivesRuntimeFailure(t *testing.T) {
	r := mustBuiltinRegistry(t)
	failure := errors.New("managed folder unavailable")
	if err := r.BindRuntime(workspace.CapabilityFileJanitor, &stubRuntime{err: failure}); err != nil {
		t.Fatalf("BindRuntime: %v", err)
	}

	status, _, err := r.Status(fileJanitorRecord(), "ws-1")
	if !errors.Is(err, failure) {
		t.Fatalf("expected the underlying error to surface, got %v", err)
	}
	if status.State != StatusNeedsAttention {
		t.Fatalf("state = %q, want needs_attention", status.State)
	}
	if status.Detail == "" {
		t.Fatal("a failed health check must still produce renderable text")
	}
}

func TestRegistry_ResolveAllKeepsUnknownRecordsVisible(t *testing.T) {
	r := mustBuiltinRegistry(t)

	resolved := r.ResolveAll([]workspace.InstalledCapability{
		fileJanitorRecord(),
		{ID: "future-capability", Version: 1, InstalledAt: time.Now(), Source: "blueprint"},
	})

	if len(resolved) != 2 {
		t.Fatalf("expected both records to be reported, got %d", len(resolved))
	}
	if !resolved[0].Available || resolved[1].Available {
		t.Fatalf("unexpected availability: %+v", resolved)
	}
	if r.ResolveAll(nil) != nil {
		t.Error("nil input should yield nil")
	}
}

func TestDefinitionClone_IsIndependent(t *testing.T) {
	r := mustBuiltinRegistry(t)

	first, _ := r.Definition(workspace.CapabilityFileJanitor)
	first.Display.Name = "mutated"
	first.Display.Highlights[0] = "mutated"
	first.Console.Tabs[0] = "mutated"
	first.Companion.DefaultDisplayName = "mutated"

	second, _ := r.Definition(workspace.CapabilityFileJanitor)
	if second.Display.Name != "File Janitor" {
		t.Fatalf("registry definition was mutated through a returned copy: %q", second.Display.Name)
	}
	if second.Display.Highlights[0] == "mutated" || second.Console.Tabs[0] == "mutated" {
		t.Fatal("returned slices share backing storage with the registry")
	}
	if second.Companion.DefaultDisplayName == "mutated" {
		t.Fatal("returned companion pointer is shared with the registry")
	}
}

func TestConsoleDescriptor_HasTabValidatesDeepLinkValues(t *testing.T) {
	def, _ := mustBuiltinRegistry(t).Definition(workspace.CapabilityFileJanitor)

	for _, tab := range []string{"review", "History", "  settings  "} {
		if !def.Console.HasTab(tab) {
			t.Errorf("expected %q to be an allowed tab", tab)
		}
	}
	for _, tab := range []string{"", "  ", "admin", "../../etc/passwd", "review;drop"} {
		if def.Console.HasTab(tab) {
			t.Errorf("expected %q to be rejected", tab)
		}
	}
}

func TestHighestPriorityStatus_FollowsRequiredOrder(t *testing.T) {
	tests := []struct {
		name  string
		input []StatusState
		want  StatusState
	}{
		{"attention beats everything", []StatusState{StatusWatching, StatusReviewReady, StatusNeedsAttention}, StatusNeedsAttention},
		{"setup beats review", []StatusState{StatusReviewReady, StatusSetupNeeded}, StatusSetupNeeded},
		{"review beats paused", []StatusState{StatusPaused, StatusReviewReady}, StatusReviewReady},
		{"paused beats watching", []StatusState{StatusWatching, StatusPaused}, StatusPaused},
		{"unlisted ranks last", []StatusState{StatusUnavailable, StatusWatching}, StatusWatching},
		{"single", []StatusState{StatusWatching}, StatusWatching},
		{"empty", nil, StatusState("")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HighestPriorityStatus(tc.input...); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
