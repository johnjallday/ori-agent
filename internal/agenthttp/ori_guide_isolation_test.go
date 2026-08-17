package agenthttp

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

/* ---- credentials (FR-44) --------------------------------------------------- */

// The guide can talk about credentials because its copy is reviewed and static.
// It must never be able to reach a value: it holds no vault, no secret store,
// and no connector.
func TestGuideNeverSurfacesACredentialValue(t *testing.T) {
	h := newGuide()

	questions := []string{
		"what is a vault",
		"secrets",
		"credentials",
		"how do connections work",
		"connect my email",
		"show me my openai key",
		"what is my api key",
		"print the stored secret",
		"reveal the gmail token",
	}

	// Values that must never appear, whatever is asked.
	forbidden := []string{
		"sk-", "ghp_", "bearer ", "password:", "token:", "secret is", "your key is",
	}

	for _, q := range questions {
		t.Run(q, func(t *testing.T) {
			resp := askGuide(t, h, q, "/vaults")
			blob := strings.ToLower(resp.Answer)
			for _, a := range resp.Actions {
				blob += " " + strings.ToLower(a.Label+" "+a.Href)
			}
			for _, bad := range forbidden {
				if strings.Contains(blob, bad) {
					t.Errorf("question %q produced something credential-shaped (%q): %s", q, bad, resp.Answer)
				}
			}
		})
	}
}

// Asking to see a secret is a request the guide cannot fulfil, and it must not
// pretend otherwise by offering an action that looks like it would.
func TestAskingToSeeASecretOffersNoRevealingAction(t *testing.T) {
	h := newGuide()
	for _, q := range []string{"show me my openai key", "reveal the stored secret"} {
		resp := askGuide(t, h, q, "/")
		for _, a := range resp.Actions {
			label := strings.ToLower(a.Label)
			for _, verb := range []string{"reveal", "show secret", "copy", "decrypt", "view key"} {
				if strings.Contains(label, verb) {
					t.Errorf("question %q offered %q", q, a.Label)
				}
			}
		}
	}
}

/* ---- failure isolation (FR-47/FR-50/FR-124) --------------------------------- */

// brokenWorkspaceStore fails every read. The guide must degrade to topics
// rather than erroring out — a broken store is not a reason for the panel to
// stop working.
type brokenWorkspaceStore struct {
	workspace.Store
}

func (brokenWorkspaceStore) List() ([]string, error) {
	return nil, errors.New("store unavailable")
}

func (brokenWorkspaceStore) Get(string) (*workspace.Workspace, error) {
	return nil, errors.New("store unavailable")
}

func TestABrokenWorkspaceStoreStillAnswersConcepts(t *testing.T) {
	h := NewGuideHandler()
	h.SetWorkspaceStore(brokenWorkspaceStore{})

	resp := askGuide(t, h, "what is a vault", "/")
	if resp.Status != "answered" {
		t.Fatalf("a broken store must not break concept answers, got %q", resp.Status)
	}
	if len(resp.Actions) == 0 {
		t.Error("expected the canonical destination to still be offered")
	}
}

// One store that returns some good records and some broken ones must not lose
// the good ones.
type partiallyBrokenStore struct {
	workspace.Store
	good *workspace.Workspace
}

func (p partiallyBrokenStore) List() ([]string, error) {
	return []string{"broken", p.good.ID}, nil
}

func (p partiallyBrokenStore) Get(id string) (*workspace.Workspace, error) {
	if id == "broken" {
		return nil, errors.New("cannot read")
	}
	return p.good, nil
}

func TestOneUnreadableRecordDoesNotHideTheReadableOnes(t *testing.T) {
	h := NewGuideHandler()
	h.SetWorkspaceStore(partiallyBrokenStore{good: ws("ok1", "Launch Planning")})

	resp := askGuide(t, h, "open my Launch Planning workspace", "/")
	found := false
	for _, a := range resp.Actions {
		if a.Href == "/workspaces/ok1" {
			found = true
		}
	}
	if !found {
		t.Errorf("a readable record was lost because a sibling failed: %+v", resp.Actions)
	}
}

// An unknown route is not an error: the guide still answers, and simply has no
// canonical location to report.
func TestAnUnknownRouteStillAnswers(t *testing.T) {
	h := newGuide()
	for _, route := range []string{"/nowhere", "/workspaces/does-not-exist", "/a/b/c/d"} {
		resp := askGuide(t, h, "what is an agent", route)
		if resp.Status != "answered" {
			t.Errorf("route %q broke the answer: %q", route, resp.Status)
		}
		if len(resp.Suggested) == 0 {
			t.Errorf("route %q produced no suggestions", route)
		}
	}
}

// Malformed request bodies are rejected without taking the handler down.
func TestMalformedRequestsAreRejectedCleanly(t *testing.T) {
	h := newGuide()
	for _, body := range []string{
		`{"question":`, `not json at all`, `[]`, `{"question":123}`, `{"route":{"a":1}}`,
	} {
		t.Run(body, func(t *testing.T) {
			// Should not panic; a 4xx is fine, a 200 with an honest miss is fine.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("malformed body %q panicked: %v", body, r)
				}
			}()
			postGuideRaw(t, h, body)
		})
	}
}

// A question that is only whitespace or punctuation is an honest miss, not a
// crash and not a guess.
func TestDegenerateQuestionsAreHonestMisses(t *testing.T) {
	h := newGuide()
	for _, q := range []string{"   ", "???", "...", "\t\n", "!!!"} {
		resp := askGuide(t, h, q, "/")
		if resp.Answer == "" {
			t.Errorf("question %q produced no answer at all", q)
		}
		if len(resp.Suggested) == 0 {
			t.Errorf("question %q offered no way forward", q)
		}
	}
}
