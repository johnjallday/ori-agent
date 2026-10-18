package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Live-model judgment matrix (checklist 5.2). It never runs by default: it
// calls a real provider only when ORI_ARRANGEMENT_LIVE=1 and an explicit
// provider, model and evidence directory are given. It drives the real Home
// planner over synthetic saved-catalog metadata (no files, no discovery) and
// records every request, response, call count, charged-token count and the
// canonical outcome for human review. Each turn is capped by the planner's own
// 8-call / 24,000-token budget. The test asserts host invariants only (nothing
// applied, no source path in any request, members are catalog entries); model
// judgment is written down, never asserted green. Scripted tests remain
// separate evidence. scripts/arrangement-live-matrix.sh is the entry point:
//
//	ORI_ARRANGEMENT_LIVE=1 ORI_ARRANGEMENT_LIVE_PROVIDER=ollama|openai|anthropic|gemini \
//	ORI_ARRANGEMENT_LIVE_MODEL=<model> ORI_ARRANGEMENT_LIVE_EVIDENCE=<dir> \
//	[ORI_ARRANGEMENT_LIVE_MUSIC_SOURCE=<clean Music worktree>] \
//	go test ./internal/projectlibrary -run TestArrangementLiveMatrix -v -timeout 90m
func TestArrangementLiveMatrix(t *testing.T) {
	if os.Getenv("ORI_ARRANGEMENT_LIVE") != "1" {
		t.Skip("live-model evaluation is opt-in (ORI_ARRANGEMENT_LIVE=1) and needs explicit approval")
	}
	providerName, model, evidence := os.Getenv("ORI_ARRANGEMENT_LIVE_PROVIDER"), os.Getenv("ORI_ARRANGEMENT_LIVE_MODEL"), os.Getenv("ORI_ARRANGEMENT_LIVE_EVIDENCE")
	if providerName == "" || model == "" || evidence == "" {
		t.Fatal("set ORI_ARRANGEMENT_LIVE_PROVIDER, ORI_ARRANGEMENT_LIVE_MODEL and ORI_ARRANGEMENT_LIVE_EVIDENCE")
	}
	live := liveArrangementProvider(t, providerName, model)
	if !workspacearrangement.SupportsMetadataPlanning(live) {
		t.Fatalf("provider %q is not a supported planning transport", providerName)
	}
	guidance, guidanceDigest := liveMusicGuidance(t, os.Getenv("ORI_ARRANGEMENT_LIVE_MUSIC_SOURCE"))
	if err := os.MkdirAll(evidence, 0o750); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"provider": providerName, "model": model, "base_url": os.Getenv("ORI_ARRANGEMENT_LIVE_BASE_URL"),
		"context_ceiling_tokens": liveContextCeiling(t), "temperature": 0.2, "max_output_tokens": workspacearrangement.MaxOutputTokens,
		"turn_token_budget": workspacearrangement.MaxTurnTokens, "turn_timeout": workspacearrangement.TurnTimeout.String(),
		"max_model_calls": workspacearrangement.MaxModelCalls, "guidance_bytes": len(guidance), "guidance_sha256": guidanceDigest,
		"music_source": os.Getenv("ORI_ARRANGEMENT_LIVE_MUSIC_SOURCE"), "music_revision": os.Getenv("ORI_ARRANGEMENT_LIVE_MUSIC_REVISION"),
		"ori_revision": os.Getenv("ORI_ARRANGEMENT_LIVE_ORI_REVISION"), "started_at": time.Now().UTC(),
		"cost": "tokens are the provider's reported counts; a local provider has no metered spend",
	}
	writeLiveJSON(t, evidence, "settings.json", settings)
	type outcome struct {
		Case, Goal, Revision           string
		Repeat, ModelCalls, Tokens     int
		Kind, Clarification, Rationale string
		Unknowns                       []string
		Operations                     []map[string]any
		Questions                      []string
		Error                          string
		Seconds                        float64
	}
	var results []outcome
	write := func(label string, repeat int, result outcome, exchanges []map[string]any) {
		results = append(results, result)
		raw, _ := json.MarshalIndent(map[string]any{"outcome": result, "provider": providerName, "model": model, "guidance_sha256": guidanceDigest, "exchanges": exchanges}, "", "  ")
		if err := os.WriteFile(filepath.Join(evidence, fmt.Sprintf("%s-%d.json", label, repeat)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// revise, when given, runs a base turn and returns the record to revise; an
	// empty ID with a note records the correction as NOT RUN instead of skipping
	// the whole matrix (t.Skip would discard every other case's summary).
	run := func(label, goal string, repeat int, revise func(*runFixture, ManagerAuthority, ArrangementRuntimeResolver) (string, int64, string)) {
		f, authority, _ := homeArrangementFixture(t)
		seedLiveCatalog(t, f)
		recorder := &recordingProvider{Provider: live}
		resolve := ArrangementRuntimeResolver(func(ctx context.Context, actual ManagerAuthority) (ArrangementRuntime, error) {
			return ArrangementRuntime{Provider: recorder, Model: model, Guidance: guidance, SkillDigest: guidanceDigest,
				ProviderFingerprint: strings.Repeat("b", 64), Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
		})
		request := ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: goal, ExpectedRevision: f.doc(t).Revision, ShareCatalogMetadata: true, AllowUpgrade: true}
		if revise != nil {
			id, revision, note := revise(f, authority, resolve)
			if id == "" {
				write(label, repeat, outcome{Case: label, Goal: goal, Repeat: repeat, Error: "NOT RUN: " + note}, recorder.exchanges())
				return
			}
			request.ReviseArrangementID, request.ExpectedRecordRevision = id, revision
			request.ExpectedRevision = f.doc(t).Revision
			recorder.reset()
		}
		before := f.doc(t)
		start := time.Now()
		planned, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, request)
		result := outcome{Case: label, Goal: goal, Repeat: repeat, Seconds: time.Since(start).Seconds()}
		// Host invariants hold whatever the model did: planning applies nothing,
		// and no source path leaves the host in a request.
		after := f.doc(t)
		if len(after.Collections) != len(before.Collections) || !reflect.DeepEqual(before.Entries, after.Entries) || !reflect.DeepEqual(before.Roots, after.Roots) {
			t.Errorf("%s/%d: planning changed catalog state", label, repeat)
		}
		for _, exchange := range recorder.exchanges() {
			if raw, _ := json.Marshal(exchange); strings.Contains(string(raw), before.Roots[0].Path) {
				t.Errorf("%s/%d: the source path left the host in a model request", label, repeat)
			}
		}
		if err != nil {
			result.Error = err.Error()
		} else {
			result.ModelCalls, result.Tokens = planned.ModelCalls, planned.ChargedTokens
			history := planned.History
			result.Kind, result.Clarification, result.Rationale = history.Kind, history.Clarification, history.Arrangement.Rationale
			result.Unknowns = history.Arrangement.Unknowns
			result.Revision = fmt.Sprint(history.Arrangement.Revision)
			names := map[string]string{}
			for _, entry := range f.doc(t).Entries {
				names[entry.ID] = entry.Fields.DisplayName
			}
			for _, saved := range f.doc(t).Arrangements {
				if saved.Record.ID != history.Arrangement.ID {
					continue
				}
				for _, op := range saved.Record.Draft.Operations {
					members := []string{}
					for _, id := range op.Members.IDs {
						name, known := names[id]
						if !known {
							t.Errorf("%s/%d: operation %q names a member that is not in the catalog: %s", label, repeat, op.Name, id)
						}
						members = append(members, name)
					}
					result.Operations = append(result.Operations, map[string]any{"kind": op.Kind, "name": op.Name, "members": members})
				}
				for _, question := range saved.Record.Draft.NeedsReview {
					result.Questions = append(result.Questions, question.Reason)
				}
			}
		}
		write(label, repeat, result, recorder.exchanges())
	}
	for repeat := 1; repeat <= 2; repeat++ {
		run("catalog-only-experiments", "Keep experiments catalog-only; separate the client songs for work", repeat, nil)
		run("albums", "Group the album projects together; leave samples and exports alone", repeat, nil)
	}
	run("next-week", "I only want to start working on the two newest client mixes next week", 1, nil)
	run("ambiguous-versions", "Pick the authoritative version of each song", 1, nil)
	run("correction", "Actually leave Client Delta out; it is a personal project", 1, func(f *runFixture, authority ManagerAuthority, resolve ArrangementRuntimeResolver) (string, int64, string) {
		first, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Separate the client songs for work", ExpectedRevision: f.doc(t).Revision, ShareCatalogMetadata: true, AllowUpgrade: true})
		if err != nil {
			return "", 0, "the base turn failed: " + err.Error()
		}
		if first.History.Kind != "proposal" {
			return "", 0, "the base turn ended as " + first.History.Kind + ", not a proposal"
		}
		return first.History.Arrangement.ID, first.History.Arrangement.Revision, ""
	})
	// No model: the host refuses before any call and keeps manual paths.
	f, _, _ := homeArrangementFixture(t)
	none := ArrangementRuntimeResolver(func(context.Context, ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{}, ErrUnavailable
	})
	_, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), none, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Organize these", ExpectedRevision: f.doc(t).Revision, ShareCatalogMetadata: true, AllowUpgrade: true})
	results = append(results, outcome{Case: "no-model", Goal: "Organize these", Repeat: 1, Error: fmt.Sprint(err)})
	writeLiveJSON(t, evidence, "summary.json", results)
	settings["finished_at"] = time.Now().UTC()
	writeLiveJSON(t, evidence, "settings.json", settings)
	var b strings.Builder
	fmt.Fprintf(&b, "# Live-model matrix: %s / %s\n\nReal planner, tools and catalog store on synthetic metadata; guidance %d bytes (sha256 %s). Judge the model by reading `<case>-<repeat>.json`; this table only summarizes.\n\n", providerName, model, len(guidance), guidanceDigest[:min(12, len(guidanceDigest))])
	b.WriteString("| Case | Repeat | Outcome | Calls | Charged tokens | Operations | Unknowns | Needs review | Seconds |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		result := r.Kind
		if r.Error != "" {
			result = "error: " + r.Error
		}
		ops := make([]string, 0, len(r.Operations))
		for _, op := range r.Operations {
			members, _ := op["members"].([]string)
			ops = append(ops, fmt.Sprintf("%s %q (%d)", op["kind"], op["name"], len(members)))
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %d | %d | %s | %d | %d | %.1f |\n", r.Case, r.Repeat, result, r.ModelCalls, r.Tokens, strings.Join(ops, "; "), len(r.Unknowns), len(r.Questions), r.Seconds)
	}
	if err := os.WriteFile(filepath.Join(evidence, "summary.md"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("live matrix: %d cases written to %s (provider %s, model %s); judge them by reading the exchanges\n%s", len(results), evidence, providerName, model, b.String())
}

func writeLiveJSON(t *testing.T, dir, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// liveContextCeiling is the num_ctx ceiling handed to a local provider. The
// planner asks for at least one bounded turn (26,048 tokens); the ceiling keeps
// a large model from allocating more than the machine can hold.
func liveContextCeiling(t *testing.T) int {
	t.Helper()
	raw := os.Getenv("ORI_ARRANGEMENT_LIVE_CONTEXT_TOKENS")
	if raw == "" {
		return 32768
	}
	n := 0
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n < workspacearrangement.MaxTurnTokens+workspacearrangement.MaxOutputTokens {
		t.Fatalf("ORI_ARRANGEMENT_LIVE_CONTEXT_TOKENS must be a number of at least %d: %q", workspacearrangement.MaxTurnTokens+workspacearrangement.MaxOutputTokens, raw)
	}
	return n
}

func liveArrangementProvider(t *testing.T, name, model string) llm.Provider {
	t.Helper()
	config := llm.ProviderConfig{Model: model, MaxTokens: workspacearrangement.MaxOutputTokens}
	switch name {
	case "ollama":
		// A local server: no key and no metered spend. Usage is Ollama's own
		// prompt_eval_count / eval_count.
		config.BaseURL = os.Getenv("ORI_ARRANGEMENT_LIVE_BASE_URL")
		config.Options = map[string]any{"max_num_ctx": liveContextCeiling(t)}
		return llm.NewOllamaProvider(config)
	case "openai":
		config.APIKey = os.Getenv("OPENAI_API_KEY")
		return requireKey(t, config, llm.NewOpenAIProvider(config))
	case "anthropic":
		config.APIKey = os.Getenv("ANTHROPIC_API_KEY")
		return requireKey(t, config, llm.NewClaudeProvider(config))
	case "gemini":
		config.APIKey = os.Getenv("GEMINI_API_KEY")
		return requireKey(t, config, llm.NewGeminiProvider(config))
	}
	t.Fatalf("unsupported live provider %q", name)
	return nil
}

func requireKey(t *testing.T, config llm.ProviderConfig, provider llm.Provider) llm.Provider {
	t.Helper()
	if config.APIKey == "" {
		t.Fatal("the provider's API key is not set in the environment")
	}
	return provider
}

// liveMusicGuidance composes guidance as the host does for the Portfolio
// Manager: the role prompt, then the parsed skill prompt. Without a source the
// matrix evaluates the generic host rules alone.
func liveMusicGuidance(t *testing.T, source string) (string, string) {
	t.Helper()
	if source == "" {
		return "", ""
	}
	skill, err := skills.ReadValidatedSkillFile(filepath.Join(source, "skills", "music-project-management", "SKILL.md"), "music-project-management")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(source, ".ori-plugin", "plugin.json")) // #nosec G304 -- an explicitly named, exported evaluation tree
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Homes []struct {
			Roles []struct {
				ID           string `json:"id"`
				SystemPrompt string `json:"system_prompt"`
			} `json:"roles"`
		} `json:"assistant_program_homes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	role := ""
	for _, home := range manifest.Homes {
		for _, candidate := range home.Roles {
			if candidate.ID == "portfolio_manager" {
				role = candidate.SystemPrompt
			}
		}
	}
	if role == "" {
		t.Fatal("the source declares no portfolio_manager role prompt")
	}
	guidance := role + "\n\n" + skill.Prompt
	sum := sha256.Sum256([]byte(guidance))
	return guidance, hex.EncodeToString(sum[:])
}

// seedLiveCatalog replaces the fixture catalog with a small mixed set of saved
// metadata: client songs, experiments, album songs, samples, exports, ambiguous
// alternate versions and a nested partial alternate mix. Names, folders and
// dates are synthetic; nothing on disk is read.
func seedLiveCatalog(t *testing.T, f *runFixture) {
	t.Helper()
	type item struct{ name, folder, format, availability string }
	items := []item{
		{"Client Alpha - Theme", "Clients/Alpha/Theme", "reaper", "available"},
		{"Client Beta - Spot", "Clients/Beta/Spot", "reaper", "available"},
		{"Client Gamma - Jingle", "Clients/Gamma/Jingle", "reaper", "available"},
		{"Client Delta - Ballad", "Clients/Delta/Ballad", "reaper", "available"},
		{"Experiment 01", "Experiments/01", "reaper", "available"},
		{"Experiment 02 granular", "Experiments/02 granular", "ableton", "available"},
		{"Experiment 03", "Experiments/03", "reaper", "available"},
		{"Experiment 04 modular", "Experiments/04 modular", "ableton", "available"},
		{"Album One - Song A", "Album One/Song A", "reaper", "available"},
		{"Album One - Song B", "Album One/Song B", "reaper", "available"},
		{"Album One - Song C", "Album One/Song C", "reaper", "available"},
		{"Song C alt mix", "Album One/Song C/Alt mix", "reaper", "available"},
		{"Album Two - Opener", "Album Two/Opener", "reaper", "available"},
		{"Album Two - Closer", "Album Two/Closer", "reaper", "available"},
		// A saved catalog only holds folders a scan recognized by a project marker,
		// so sample/export folders carry a real format too; their names and folders
		// are the signal, as in a real library.
		{"Samples - Drums", "Samples/Drums", "reaper", "available"},
		{"Samples - Vocal chops", "Samples/Vocal chops", "reaper", "available"},
		{"Exports 2026", "Exports/2026", "reaper", "available"},
		{"Bounce stems", "Exports/Bounce stems", "reaper", "available"},
		{"Song X final", "Song X/final", "reaper", "ambiguous"},
		{"Song X final v2", "Song X/final v2", "reaper", "available"},
		{"Song X old", "Song X/old", "reaper", "available"},
		{"Old sketch (backup)", "Archive/Old sketch", "reaper", "unavailable"},
	}
	_, _, err := f.library.mutate(f.scope, f.doc(t).Revision, operation{key: "live-fixture", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		seed := doc.Entries[0]
		doc.Entries = nil
		for i, it := range items {
			entry := seed
			entry.ID = fmt.Sprintf("live-%02d", i)
			entry.Fields.DisplayName = it.name
			observation := seed.Observations[0]
			observation.RelativeFolder, observation.FileIdentity, observation.Alternates = it.folder, fmt.Sprintf("live-identity-%02d", i), nil
			observation.Format, observation.Availability = it.format, it.availability
			if it.availability == "ambiguous" {
				observation.Alternates = []string{"final.rpp", "final alt.RPP"}
			}
			observation.ScannedAt = observation.ScannedAt.Add(-time.Duration(len(items)-i) * 24 * time.Hour)
			observation.FileModifiedAt = observation.ScannedAt.Add(-time.Duration(i%7) * 24 * time.Hour)
			entry.Observations = []Observation{observation}
			doc.Entries = append(doc.Entries, entry)
		}
		return "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// recordingProvider keeps every exchange for the evidence file; it changes
// nothing about the request or the reply.
type recordingProvider struct {
	llm.Provider
	mu  sync.Mutex
	log []map[string]any
}

func (p *recordingProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	started := time.Now()
	response, err := p.Provider.Chat(ctx, request)
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := map[string]any{"request": request, "duration_ms": time.Since(started).Milliseconds()}
	if response != nil {
		entry["response"] = response
	}
	if err != nil {
		entry["error"] = err.Error()
	}
	p.log = append(p.log, entry)
	return response, err
}

func (p *recordingProvider) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = nil
}

func (p *recordingProvider) exchanges() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.log...)
}
