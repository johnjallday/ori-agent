package settingsreset

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// Canonical v1 receipt compatibility.
//
// decodeJournal accepts a stored receipt only when re-encoding the decoded value
// reproduces the stored bytes exactly. That makes the encoded shape of a v1
// journal a hard compatibility contract: any new field that is not omitted from
// a payload which does not use it strands every receipt already on disk.
//
// These tests freeze that shape. The Go literals below describe receipts exactly
// as the shipped v1 encoder wrote them, and testdata holds the bytes. Neither
// validateJournal nor decodeJournal touches the filesystem, so the fixtures use
// a synthetic installation root and need nothing to exist.
//
// Regenerate deliberately with ORI_RESET_GOLDEN_UPDATE=1 and review the diff:
// a changed golden file means stored receipts change meaning.

const compatRoot = "/ori-installation-compat-fixture"

func compatDigest(seed byte) string {
	return strings.Repeat(string(rune('a'+seed%6)), 64)
}

func compatRestart() RestartInfo {
	return RestartInfo{
		Mode:         RestartProcessRelaunch,
		Instructions: "Fully stop and relaunch Ori using the same installation. In menubar, Quit the application; Stop/Start Server is insufficient.",
	}
}

func compatRetained() []Location {
	return []Location{
		{DisplayPath: filepath.Join(compatRoot, "retained-projects"), Reason: "Retain workspace/project or vault backing files, including nested contents and encryption material."},
		{DisplayPath: "Environment, external CLI authentication and third-party accounts", Reason: "Not removed or revoked. External credentials may still make a provider available."},
	}
}

// compatLegacyTargets mirrors the v1 production target paths for every kind a
// category resolves, using the synthetic root.
func compatLegacyTargets(selected []CategoryID) []resolvedTarget {
	paths := map[string]string{
		"settings_fields":               filepath.Join(compatRoot, "settings.json"),
		"agent_index":                   filepath.Join(compatRoot, "agents.json"),
		"agent_profiles":                filepath.Join(compatRoot, "agents"),
		"agent_projection":              filepath.Join(compatRoot, "agents.json"),
		"workspace_registration_fields": filepath.Join(compatRoot, "settings.json"),
		"workspace_permissions":         filepath.Join(compatRoot, "workspace_allowlist.json"),
		"database_records":              filepath.Join(compatRoot, "sessions.db"),
		"owned_uploads":                 filepath.Join(compatRoot, "session_files"),
		"setup_fields":                  filepath.Join(compatRoot, "app_state.json"),
		"first_run_state":               filepath.Join(compatRoot, "app_state.json"),
		"model_categories":              filepath.Join(compatRoot, "model_categories.json"),
		"location_zones":                filepath.Join(compatRoot, "locations.json"),
		"connection_metadata":           filepath.Join(compatRoot, "connections", "google.json"),
		"connection_consent":            filepath.Join(compatRoot, "connections", "consent.json"),
		"mcp_registry":                  filepath.Join(compatRoot, "mcp_registry.json"),
		"mcp_search_sources":            filepath.Join(compatRoot, "mcp_search_sources.json"),
		"mcp_search_cache":              filepath.Join(compatRoot, "mcp_search_cache.json"),
		"plugin_registry":               filepath.Join(compatRoot, "plugins", "installed.json"),
		"plugin_marketplaces":           filepath.Join(compatRoot, "plugins", "marketplaces.json"),
		"plugin_clones":                 filepath.Join(compatRoot, "plugins", "src"),
		"plugin_state":                  filepath.Join(compatRoot, "plugins", "state"),
		"plugin_artifacts":              filepath.Join(compatRoot, "plugins", "artifacts"),
		"plugin_preview":                filepath.Join(compatRoot, "plugins", "preview"),
		"project_templates":             filepath.Join(compatRoot, "templates"),
		"post_reset_project_templates":  filepath.Join(compatRoot, "templates-default"),
		"workflow_templates":            filepath.Join(compatRoot, "workflow_templates"),
		"usage_records":                 filepath.Join(compatRoot, "usage_data"),
		"activity_logs":                 filepath.Join(compatRoot, "activity_logs"),
		"cli_event_logs":                filepath.Join(compatRoot, "cli_agent_tasks"),
		"cli_mcp_configs":               filepath.Join(compatRoot, "cli-mcp"),
	}
	targets := []resolvedTarget{}
	for _, id := range selected {
		for _, kind := range targetKinds(id) {
			targets = append(targets, resolvedTarget{Category: id, Kind: kind, Path: paths[kind]})
		}
	}
	return targets
}

// compatJournal builds a receipt exactly as v1 wrote it. outcome applies to every
// category, so the caller picks a coherent operation state.
func compatJournal(intent Intent, selected []CategoryID, state OperationState, outcome Outcome, revision uint64) *journal {
	created := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	preview := Preview{
		SchemaVersion: SchemaVersion, ID: "compat-preview-id", OperationID: "compat-operation-id",
		Intent: intent, Selected: slices.Clone(selected),
		Dependencies: []CategoryID{}, Categories: []CategoryPreview{}, Blockers: []Blocker{},
		ScopeDigest: compatDigest(0), ExpiresAt: created.Add(10 * time.Minute), Restart: compatRestart(),
	}
	operation := Operation{
		SchemaVersion: SchemaVersion, ID: "compat-operation-id", Intent: intent, State: state,
		Revision: revision, Results: []CategoryResult{}, Blockers: []Blocker{}, Restart: compatRestart(),
		CreatedAt: created, UpdatedAt: created.Add(time.Minute),
	}
	count := int64(2)
	for _, id := range selected {
		def, _ := definition(id)
		preview.Categories = append(preview.Categories, CategoryPreview{
			ID: id, Label: def.Label, Description: def.Description,
			Facts:    []CountFact{{Name: "compat fact", Count: &count}},
			Removed:  []Location{{DisplayPath: filepath.Join(compatRoot, "owned"), Reason: "Remove this enumerated Ori-owned state; preserve unknown and external files."}},
			Retained: compatRetained(),
		})
		result := CategoryResult{ID: id, Outcome: outcome, Checks: []CheckResult{}, Retained: compatRetained()}
		switch outcome {
		case OutcomeFailed, OutcomeUnknown:
			result.Retryable, result.Message = true, "One or more named postconditions could not be verified."
		}
		for _, name := range def.Checks {
			check := CheckResult{Name: name, Outcome: outcome}
			if outcome == OutcomeFailed || outcome == OutcomeUnknown {
				check.Message = "Postcondition was not verified."
			}
			result.Checks = append(result.Checks, check)
		}
		operation.Results = append(operation.Results, result)
	}
	protected := []string{filepath.Join(compatRoot, "retained-projects"), filepath.Join(compatRoot, "retained-vaults")}
	sort.Strings(protected)
	digests := make([]protectedDigest, 0, len(protected))
	for index, path := range protected {
		digests = append(digests, protectedDigest{Path: path, Digest: compatDigest(byte(index + 1))})
	}
	return &journal{
		Version: SchemaVersion, AcceptedBy: "compat-accepted-by", RequestID: "compat-request-id",
		Plan: resolvedPlan{
			Preview: preview, Targets: compatLegacyTargets(selected), Installation: compatRoot,
			Evidence: resolvedEvidence{
				ProtectedPaths: protected, ProtectedDigests: digests,
				DatabasePath: filepath.Join(compatRoot, "sessions.db"), DatabaseSchema: compatDigest(3),
				DatabaseSchemaVersion: 42, WorkspaceRootConfirmed: true,
			},
		},
		Operation: operation,
	}
}

type compatCase struct {
	name     string
	journal  func() *journal
	expected OperationState
}

func compatCases() []compatCase {
	selectedFour := []CategoryID{CategoryAgents, CategoryAppRecords, CategorySettings, CategorySetupSteps}
	fresh, _ := Selection(IntentStartFresh, nil)
	return []compatCase{
		{"selected-awaiting-restart", func() *journal {
			return compatJournal(IntentSelectedData, []CategoryID{CategorySetupSteps}, StateAwaitingRestart, OutcomePending, 2)
		}, StateAwaitingRestart},
		{"selected-four-categories-completed", func() *journal {
			return compatJournal(IntentSelectedData, selectedFour, StateCompleted, OutcomeCompleted, 7)
		}, StateCompleted},
		{"selected-partial-failure", func() *journal {
			return compatJournal(IntentSelectedData, selectedFour, StatePartialFailure, OutcomeFailed, 9)
		}, StatePartialFailure},
		{"start-fresh-awaiting-restart", func() *journal {
			return compatJournal(IntentStartFresh, fresh, StateAwaitingRestart, OutcomePending, 2)
		}, StateAwaitingRestart},
		{"start-fresh-applying", func() *journal {
			return compatJournal(IntentStartFresh, fresh, StateApplying, OutcomePending, 3)
		}, StateApplying},
		{"start-fresh-completed", func() *journal {
			return compatJournal(IntentStartFresh, fresh, StateCompleted, OutcomeCompleted, 12)
		}, StateCompleted},
		{"start-fresh-partial-failure", func() *journal {
			return compatJournal(IntentStartFresh, fresh, StatePartialFailure, OutcomeUnknown, 14)
		}, StatePartialFailure},
		{"start-fresh-blocked", func() *journal {
			j := compatJournal(IntentStartFresh, fresh, StateBlocked, OutcomePending, 2)
			j.Operation.Blockers = []Blocker{{
				Code: "drain_failed", Message: "Reset could not finish safe admission; no selected data was deleted.",
				Recovery: "Keep ordinary work stopped. Fully quit and relaunch into recovery; do not delete reset metadata or blindly repeat the request.",
			}}
			return j
		}, StateBlocked},
	}
}

func compatGoldenPath(name string) string {
	return filepath.Join("testdata", "journal-v1-"+name+".json")
}

// TestCanonicalV1ReceiptsRemainByteCompatible is the compatibility gate. Adding a
// field to any reset type must leave these stored bytes decodable, re-encodable
// byte-for-byte, and valid in their original state.
func TestCanonicalV1ReceiptsRemainByteCompatible(t *testing.T) {
	update := os.Getenv("ORI_RESET_GOLDEN_UPDATE") == "1"
	for _, testCase := range compatCases() {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := encodeJournal(testCase.journal())
			if err != nil {
				t.Fatalf("the v1 receipt shape no longer validates: %v", err)
			}
			path := compatGoldenPath(testCase.name)
			if update {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatalf("create testdata: %v", err)
				}
				if err := os.WriteFile(path, encoded, 0o600); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			golden, err := os.ReadFile(path) // #nosec G304 -- checked-in test fixture
			if err != nil {
				t.Fatalf("read golden (regenerate with ORI_RESET_GOLDEN_UPDATE=1 and review): %v", err)
			}
			if !bytes.Equal(golden, encoded) {
				t.Fatal("the canonical encoding of a v1 receipt changed; every stored receipt with this shape would be stranded")
			}
			decoded, err := decodeJournal(golden)
			if err != nil {
				t.Fatalf("a stored v1 receipt no longer decodes: %v", err)
			}
			if decoded.Operation.State != testCase.expected {
				t.Fatalf("state changed meaning: %s", decoded.Operation.State)
			}
			reencoded, err := encodeJournal(decoded)
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			if !bytes.Equal(golden, reencoded) {
				t.Fatal("decode/re-encode is no longer byte-identical")
			}
		})
	}
}

// TestCanonicalV1ReceiptsGainNoNewKeys states the additive rule directly: a
// receipt that uses no new capability must serialize exactly the v1 key set.
// A new field without omitempty (or on a type omitempty ignores, such as a
// struct) shows up here before it can reach a user's installation.
func TestCanonicalV1ReceiptsGainNoNewKeys(t *testing.T) {
	for _, testCase := range compatCases() {
		t.Run(testCase.name, func(t *testing.T) {
			golden, err := os.ReadFile(compatGoldenPath(testCase.name)) // #nosec G304 -- checked-in test fixture
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			encoded, err := encodeJournal(testCase.journal())
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			want, got := jsonKeyPaths(t, golden), jsonKeyPaths(t, encoded)
			if !slices.Equal(want, got) {
				t.Fatalf("the v1 key set changed.\nstored: %v\ncurrent: %v", want, got)
			}
		})
	}
}

// TestCanonicalPolicyRecordRemainsByteCompatible covers the second private
// record. Its encoder has the same byte-exact contract as the journal's.
func TestCanonicalPolicyRecordRemainsByteCompatible(t *testing.T) {
	policy := StartupPolicy{
		SchemaVersion: SchemaVersion, OperationID: "compat-operation-id",
		SuppressWorkspaceAdoption: true, SuppressAgentRehydration: true,
		SuppressProfileSeed: true, SuppressExternalMCPImport: true,
		UpdatedAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
	}
	encoded, err := encodeStartupPolicy(policy)
	if err != nil {
		t.Fatalf("encode policy: %v", err)
	}
	const golden = `{"schema_version":1,"operation_id":"compat-operation-id","suppress_workspace_adoption":true,"suppress_agent_rehydration":true,"suppress_profile_seed":true,"suppress_external_mcp_import":true,"updated_at":"2026-03-01T12:00:00Z"}`
	if string(encoded) != golden {
		t.Fatalf("the canonical policy encoding changed; stored policies would be stranded:\n%s", encoded)
	}
}

// jsonKeyPaths returns every dotted key path in a document, sorted. Array
// elements collapse to a single "[]" step so repeated members do not inflate it.
func jsonKeyPaths(t *testing.T, data []byte) []string {
	t.Helper()
	var document any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode for key comparison: %v", err)
	}
	found := make(map[string]bool)
	var walk func(prefix string, value any)
	walk = func(prefix string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				path := key
				if prefix != "" {
					path = prefix + "." + key
				}
				found[path] = true
				walk(path, child)
			}
		case []any:
			for _, child := range typed {
				walk(prefix+".[]", child)
			}
		}
	}
	walk("", document)
	paths := make([]string, 0, len(found))
	for path := range found {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
