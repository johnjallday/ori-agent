package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Live-model judgment matrix (checklist 5.2). It never runs by default: it
// calls a paid provider only when ORI_ARRANGEMENT_LIVE=1 and an explicit
// provider, model and evidence directory are given. It drives the real Home
// planner over synthetic saved-catalog metadata (no files, no discovery) and
// records every request, response, call count, charged-token count and the
// canonical outcome for human review. Each turn is capped by the planner's own
// 8-call / 24,000-token budget. Scripted tests remain separate evidence.
//
//	ORI_ARRANGEMENT_LIVE=1 ORI_ARRANGEMENT_LIVE_PROVIDER=openai \
//	ORI_ARRANGEMENT_LIVE_MODEL=<model> ORI_ARRANGEMENT_LIVE_EVIDENCE=<dir> \
//	[ORI_ARRANGEMENT_LIVE_MUSIC_SOURCE=<exported clean Music tree>] \
//	go test -short ./internal/projectlibrary -run TestArrangementLiveMatrix -v -timeout 30m
func TestArrangementLiveMatrix(t *testing.T) {
	if os.Getenv("ORI_ARRANGEMENT_LIVE") != "1" {
		t.Skip("live-model evaluation is opt-in (ORI_ARRANGEMENT_LIVE=1) and needs explicit approval")
	}
	providerName, model, evidence := os.Getenv("ORI_ARRANGEMENT_LIVE_PROVIDER"), os.Getenv("ORI_ARRANGEMENT_LIVE_MODEL"), os.Getenv("ORI_ARRANGEMENT_LIVE_EVIDENCE")
	if providerName == "" || model == "" || evidence == "" {
		t.Fatal("set ORI_ARRANGEMENT_LIVE_PROVIDER, ORI_ARRANGEMENT_LIVE_MODEL and ORI_ARRANGEMENT_LIVE_EVIDENCE")
	}
	live := liveArrangementProvider(t, providerName, model)
	guidance, guidanceDigest := liveMusicGuidance(t, os.Getenv("ORI_ARRANGEMENT_LIVE_MUSIC_SOURCE"))
	if err := os.MkdirAll(evidence, 0o750); err != nil {
		t.Fatal(err)
	}
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
	run := func(label, goal string, repeat int, revise func(*runFixture, ManagerAuthority, ArrangementRuntimeResolver) (string, int64)) {
		f, authority, _ := homeArrangementFixture(t)
		seedLiveCatalog(t, f)
		recorder := &recordingProvider{Provider: live}
		resolve := ArrangementRuntimeResolver(func(ctx context.Context, actual ManagerAuthority) (ArrangementRuntime, error) {
			return ArrangementRuntime{Provider: recorder, Model: model, Guidance: guidance, SkillDigest: guidanceDigest,
				ProviderFingerprint: strings.Repeat("b", 64), Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
		})
		request := ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: goal, ExpectedRevision: f.doc(t).Revision, ShareCatalogMetadata: true, AllowUpgrade: true}
		if revise != nil {
			request.ReviseArrangementID, request.ExpectedRecordRevision = revise(f, authority, resolve)
			request.ExpectedRevision = f.doc(t).Revision
			recorder.reset()
		}
		start := time.Now()
		planned, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, request)
		result := outcome{Case: label, Goal: goal, Repeat: repeat, Seconds: time.Since(start).Seconds()}
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
						members = append(members, names[id])
					}
					result.Operations = append(result.Operations, map[string]any{"kind": op.Kind, "name": op.Name, "members": members})
				}
				for _, question := range saved.Record.Draft.NeedsReview {
					result.Questions = append(result.Questions, question.Reason)
				}
			}
		}
		results = append(results, result)
		raw, _ := json.MarshalIndent(map[string]any{"outcome": result, "provider": providerName, "model": model, "guidance_sha256": guidanceDigest, "exchanges": recorder.exchanges()}, "", "  ")
		if err := os.WriteFile(filepath.Join(evidence, fmt.Sprintf("%s-%d.json", label, repeat)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for repeat := 1; repeat <= 2; repeat++ {
		run("catalog-only-experiments", "Keep experiments catalog-only; separate the client songs for work", repeat, nil)
		run("albums", "Group the album projects together; leave samples and exports alone", repeat, nil)
	}
	run("next-week", "I only want to start working on the two newest client mixes next week", 1, nil)
	run("ambiguous-versions", "Pick the authoritative version of each song", 1, nil)
	run("correction", "Actually leave Client Delta out; it is a personal project", 1, func(f *runFixture, authority ManagerAuthority, resolve ArrangementRuntimeResolver) (string, int64) {
		first, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Separate the client songs for work", ExpectedRevision: f.doc(t).Revision, ShareCatalogMetadata: true, AllowUpgrade: true})
		if err != nil || first.History.Kind == "clarification" {
			t.Skipf("correction base did not produce a proposal: %v", err)
		}
		return first.History.Arrangement.ID, first.History.Arrangement.Revision
	})
	// No model: the host refuses before any call and keeps manual paths.
	f, _, _ := homeArrangementFixture(t)
	none := ArrangementRuntimeResolver(func(context.Context, ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{}, ErrUnavailable
	})
	_, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), none, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Organize these", ExpectedRevision: f.doc(t).Revision, ShareCatalogMetadata: true, AllowUpgrade: true})
	results = append(results, outcome{Case: "no-model", Goal: "Organize these", Repeat: 1, Error: fmt.Sprint(err)})
	raw, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filepath.Join(evidence, "summary.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("live matrix: %d cases written to %s (provider %s, model %s); judge them by reading the exchanges", len(results), evidence, providerName, model)
}

func liveArrangementProvider(t *testing.T, name, model string) llm.Provider {
	t.Helper()
	config := llm.ProviderConfig{Model: model, MaxTokens: workspacearrangement.MaxOutputTokens}
	switch name {
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
		} `json:"homes"`
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
// metadata: client songs, experiments, album songs, samples, exports and
// ambiguous alternate versions. Names and dates are synthetic.
func seedLiveCatalog(t *testing.T, f *runFixture) {
	t.Helper()
	names := []string{
		"Client Alpha - Theme", "Client Beta - Spot", "Client Gamma - Jingle", "Client Delta - Ballad",
		"Experiment 01", "Experiment 02 granular", "Experiment 03", "Experiment 04 modular",
		"Album One - Song A", "Album One - Song B", "Album One - Song C", "Album Two - Opener", "Album Two - Closer",
		"Samples - Drums", "Samples - Vocal chops", "Exports 2026", "Bounce stems",
		"Song X final", "Song X final v2", "Song X old", "Old sketch (backup)",
	}
	_, _, err := f.library.mutate(f.scope, f.doc(t).Revision, operation{key: "live-fixture", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		seed := doc.Entries[0]
		doc.Entries = nil
		for i, name := range names {
			entry := seed
			entry.ID = fmt.Sprintf("live-%02d", i)
			entry.Fields.DisplayName = name
			observation := seed.Observations[0]
			observation.RelativeFolder, observation.FileIdentity, observation.Alternates = name, fmt.Sprintf("live-identity-%02d", i), nil
			observation.ScannedAt = observation.ScannedAt.Add(-time.Duration(len(names)-i) * 24 * time.Hour)
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
	response, err := p.Provider.Chat(ctx, request)
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := map[string]any{"request": request}
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
