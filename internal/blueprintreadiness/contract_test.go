package blueprintreadiness

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeCollapsesUnknownEnumsToTheSafeClassification(t *testing.T) {
	got := Readiness{
		State:     State("totally-made-up"),
		Ownership: Ownership("vendor"),
		Reason:    Reason("because"),
	}.Normalize()

	// An unrecognized value must never be passed through: a client that does
	// not know it would fall back to rendering the card as ordinary.
	if got.State != StateUnavailable {
		t.Fatalf("unknown state = %q, want %q", got.State, StateUnavailable)
	}
	if got.Ownership != OwnershipPlugin {
		t.Fatalf("unknown ownership = %q, want %q", got.Ownership, OwnershipPlugin)
	}
	if got.Reason != ReasonDependencyStateUnknown {
		t.Fatalf("unknown reason = %q, want %q", got.Reason, ReasonDependencyStateUnknown)
	}
}

func TestNormalizeStripsEverythingReadyMustNotCarry(t *testing.T) {
	got := Readiness{
		State:      StateReady,
		Ownership:  OwnershipUser,
		Reason:     ReasonPluginEnableRequired,
		Summary:    "All set.",
		Diagnostic: "invalid setup wizard: unregistered adapter",
		Actions:    []Action{ActionEnablePlugin},
	}.Normalize()

	if got.Reason != ReasonNone || got.Diagnostic != "" || len(got.Actions) != 0 {
		t.Fatalf("ready projection kept blocker data: %+v", got)
	}
	if !got.Creatable() {
		t.Fatal("ready projection is not creatable")
	}
	if got.Summary != "All set." {
		t.Fatalf("ready projection lost its copy: %q", got.Summary)
	}
}

func TestNormalizeKeepsAuthorDiagnosticsForUserTemplatesOnly(t *testing.T) {
	diagnostic := "invalid setup wizard: step s has unknown kind widget"
	for _, tc := range []struct {
		ownership Ownership
		want      string
	}{
		{OwnershipUser, diagnostic},
		{OwnershipBuiltin, ""},
		{OwnershipPlugin, ""},
	} {
		got := Readiness{
			State: StateUnavailable, Ownership: tc.ownership,
			Reason: ReasonManifestInvalid, Diagnostic: diagnostic,
		}.Normalize()
		if got.Diagnostic != tc.want {
			t.Errorf("%s diagnostic = %q, want %q", tc.ownership, got.Diagnostic, tc.want)
		}
	}
}

func TestNormalizeActionsAreAllowlistedDeduplicatedAndOwnershipScoped(t *testing.T) {
	got := Readiness{
		State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginEnableRequired,
		Actions: []Action{
			ActionEnablePlugin,
			ActionEnablePlugin,         // duplicate
			Action("run_shell"),        // not on the allowlist
			ActionEditTemplateManifest, // wrong owner
			ActionManagePlugins, ActionChangeBlueprint,
		},
	}.Normalize()

	want := []Action{ActionEnablePlugin, ActionManagePlugins, ActionChangeBlueprint}
	if len(got.Actions) != len(want) {
		t.Fatalf("actions = %v, want %v", got.Actions, want)
	}
	for i := range want {
		if got.Actions[i] != want[i] {
			t.Fatalf("actions = %v, want %v", got.Actions, want)
		}
	}
}

func TestNormalizeCapsTheActionList(t *testing.T) {
	got := Readiness{
		State: StateActionRequired, Ownership: OwnershipUser, Reason: ReasonManifestInvalid,
		Actions: []Action{
			ActionEditTemplateManifest, ActionManagePlugins, ActionChangeBlueprint,
			ActionRetry, ActionInstallPlugin, ActionEnablePlugin,
		},
	}.Normalize()
	if len(got.Actions) != MaxActions {
		t.Fatalf("actions = %v, want %d of them", got.Actions, MaxActions)
	}
}

func TestParseActionRefusesAnythingOffTheAllowlist(t *testing.T) {
	for _, raw := range []string{
		"install_plugin", "enable_plugin", "review_plugin_update",
		"retry", "manage_plugins", "change_blueprint", "edit_template_manifest",
	} {
		if _, ok := ParseAction(raw); !ok {
			t.Errorf("allowlisted action %q was refused", raw)
		}
	}
	for _, raw := range []string{
		"", " install_plugin", "INSTALL_PLUGIN", "install_plugin ", "uninstall_plugin",
		"install_plugin;rm -rf /", "../install_plugin", "grant_permissions",
	} {
		if action, ok := ParseAction(raw); ok {
			t.Errorf("action %q was accepted as %q", raw, action)
		}
	}
}

func TestSanitizeCopyRedactsLocatorsAndControlCharacters(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "url",
			in:   "Install it from https://evil.test/payload.sh first",
			want: "Install it from … first",
		},
		{
			name: "absolute path",
			in:   "artifact missing at /Users/someone/Library/ori/plugin.bin",
			want: "artifact missing at …",
		},
		{
			name: "home relative path",
			in:   "open ~/Downloads/setup.command to continue",
			want: "open … to continue",
		},
		{
			name: "windows path",
			in:   `run C:\Users\x\setup.exe now`,
			want: "run … now",
		},
		{
			name: "shell expansion",
			in:   "value is $(whoami) today",
			want: "value is … today",
		},
		{
			name: "control characters and newlines collapse",
			in:   "line one\n\tline\x00 two",
			want: "line one line two",
		},
		{
			name: "ordinary copy is untouched",
			in:   "The plugin is installed but disabled.",
			want: "The plugin is installed but disabled.",
		},
	} {
		if got := SanitizeCopy(tc.in, MaxDetailLen); got != tc.want {
			t.Errorf("%s: SanitizeCopy(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestSanitizeCopyTruncatesOnRuneBoundaries(t *testing.T) {
	long := strings.Repeat("é", 500)
	got := SanitizeCopy(long, MaxSummaryLen)
	if utf8.RuneCountInString(got) != MaxSummaryLen {
		t.Fatalf("length = %d runes, want %d", utf8.RuneCountInString(got), MaxSummaryLen)
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncation produced invalid UTF-8")
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated copy is not marked as truncated: %q", got)
	}
}

func TestNormalizeTruncatesEveryCopyField(t *testing.T) {
	got := Readiness{
		State: StateUnavailable, Ownership: OwnershipUser, Reason: ReasonManifestInvalid,
		Summary:    strings.Repeat("a", 1000),
		Detail:     strings.Repeat("b", 1000),
		Diagnostic: strings.Repeat("c", 1000),
	}.Normalize()

	if utf8.RuneCountInString(got.Summary) != MaxSummaryLen {
		t.Errorf("summary = %d runes", utf8.RuneCountInString(got.Summary))
	}
	if utf8.RuneCountInString(got.Detail) != MaxDetailLen {
		t.Errorf("detail = %d runes", utf8.RuneCountInString(got.Detail))
	}
	if utf8.RuneCountInString(got.Diagnostic) != MaxDiagnosticLen {
		t.Errorf("diagnostic = %d runes", utf8.RuneCountInString(got.Diagnostic))
	}
}

// TestDependencyNeverSerializesASource is the contract's central disclosure
// promise: a template-declared plugin source is an untrusted hint, reported
// only as present. The trust preview is the one surface allowed to show it.
func TestDependencyNeverSerializesASource(t *testing.T) {
	encoded, err := json.Marshal(Readiness{
		State: StateActionRequired, Ownership: OwnershipUser, Reason: ReasonPluginInstallRequired,
		Dependency: &Dependency{PluginName: "owner-plugin", SourceDeclared: true},
		Actions:    []Action{ActionInstallPlugin},
	}.Normalize())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	dependency, _ := fields["dependency"].(map[string]any)
	if dependency == nil {
		t.Fatalf("dependency missing: %s", encoded)
	}
	for key := range dependency {
		switch key {
		case "plugin_name", "plugin_version", "installed", "enabled", "source_declared":
		default:
			t.Errorf("dependency exposes unexpected field %q: %s", key, encoded)
		}
	}
	if strings.Contains(string(encoded), "://") {
		t.Errorf("serialized readiness contains a locator: %s", encoded)
	}
}

func TestReadyHelperProducesACreatableProjection(t *testing.T) {
	for _, ownership := range []Ownership{OwnershipBuiltin, OwnershipUser, OwnershipPlugin} {
		got := Ready(ownership).Normalize()
		if !got.Creatable() || got.Reason != ReasonNone || len(got.Actions) != 0 {
			t.Errorf("Ready(%s) = %+v", ownership, got)
		}
	}
}
