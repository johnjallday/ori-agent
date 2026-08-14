package workspaceplan

import (
	"testing"
)

func hashableContent() PlanContent {
	return PlanContent{
		Rationale:   "Because it is safer",
		Explanation: "Some prose for the reader",
		InScope:     []string{"reporting"},
		NonGoals:    []string{"billing"},
		Assumptions: []Assumption{{ID: "asm-1", Statement: "Staging mirrors production", Author: AuthorModel}},
		Risks:       []Risk{{ID: "rsk-1", Statement: "Row drift", Severity: RiskMedium, Mitigation: "Checksums"}},
		Sources:     []Source{{ID: "src-1", Kind: SourceFile, Ref: "docs/schema.md", Title: "Schema", Excerpt: "..."}},
		Artifacts: []ProposedArtifact{{
			ID: "art-1", Kind: ArtifactPRD, Path: "tasks/prd.md",
			Title: "The PRD", Description: "A description", Enabled: true,
		}},
		Clarifications: []Clarification{{
			ID: "clr-1", Prompt: "Which environment?", Detail: "extra detail",
			Options: []string{"a", "b"}, Required: true,
			Status: ClarificationAnswered, Answer: "Staging",
		}},
		Validations: []ValidationCheckpoint{{
			ID: "val-1", Title: "Row counts match", Required: true,
			AppliesTo: []string{"grp-1"}, Expectation: "counts equal",
		}},
		Execution: ExecutionPolicy{Mode: ExecutionStepThrough, Preconditions: []string{"repo_scan"}},
		Groups: []TaskGroup{{
			ID: "grp-1", Title: "Prepare", Outcome: "Ready", Notes: "some notes", Author: AuthorModel,
			Items: []TaskItem{
				{ID: "itm-1", Description: "Snapshot", Assignee: "builder", Author: AuthorModel},
				{ID: "itm-2", Description: "Verify", DependsOn: []string{"itm-1"}, Author: AuthorUser},
			},
		}},
	}
}

func mustHash(t *testing.T, objective string, content PlanContent, policy PolicySnapshot) string {
	t.Helper()
	hash, err := ContentHash(objective, content, policy)
	if err != nil {
		t.Fatalf("content hash: %v", err)
	}
	return hash
}

func TestContentHashIsStableAndDeterministic(t *testing.T) {
	content := hashableContent()
	first := mustHash(t, "Objective", content, PolicySnapshot{})

	for range 20 {
		if again := mustHash(t, "Objective", content, PolicySnapshot{}); again != first {
			t.Fatalf("hash changed between runs: %s vs %s", first, again)
		}
	}

	// An independently built but equal plan hashes identically.
	if other := mustHash(t, "Objective", hashableContent(), PolicySnapshot{}); other != first {
		t.Errorf("equal content hashed differently: %s vs %s", first, other)
	}
}

// A nil slice and an empty slice mean the same thing, and a serializer
// round-trip must not force a re-approval.
func TestContentHashTreatsNilAndEmptySlicesAlike(t *testing.T) {
	withNil := hashableContent()
	withNil.Groups[0].Items[0].DependsOn = nil
	withEmpty := hashableContent()
	withEmpty.Groups[0].Items[0].DependsOn = []string{}

	if mustHash(t, "o", withNil, PolicySnapshot{}) != mustHash(t, "o", withEmpty, PolicySnapshot{}) {
		t.Error("nil and empty dependency lists hashed differently")
	}
}

// Everything that changes what work happens, or what the user agreed to, must
// change the hash (FR-33).
func TestApprovalRelevantChangesAlterTheHash(t *testing.T) {
	base := mustHash(t, "Objective", hashableContent(), PolicySnapshot{})

	cases := map[string]func(*PlanContent, *string){
		"objective":            func(_ *PlanContent, o *string) { *o = "A different objective" },
		"in scope":             func(c *PlanContent, _ *string) { c.InScope = []string{"everything"} },
		"non-goals":            func(c *PlanContent, _ *string) { c.NonGoals = nil },
		"assumption statement": func(c *PlanContent, _ *string) { c.Assumptions[0].Statement = "changed" },
		"risk severity":        func(c *PlanContent, _ *string) { c.Risks[0].Severity = RiskHigh },
		"source ref":           func(c *PlanContent, _ *string) { c.Sources[0].Ref = "docs/other.md" },
		"artifact path":        func(c *PlanContent, _ *string) { c.Artifacts[0].Path = "tasks/other.md" },
		"artifact enabled":     func(c *PlanContent, _ *string) { c.Artifacts[0].Enabled = false },
		"group title":          func(c *PlanContent, _ *string) { c.Groups[0].Title = "Renamed" },
		"group outcome":        func(c *PlanContent, _ *string) { c.Groups[0].Outcome = "Different" },
		"group dependency":     func(c *PlanContent, _ *string) { c.Groups[0].DependsOn = []string{"grp-x"} },
		"item description":     func(c *PlanContent, _ *string) { c.Groups[0].Items[0].Description = "Different" },
		"item details":         func(c *PlanContent, _ *string) { c.Groups[0].Items[0].Details = "Added detail" },
		"item assignee":        func(c *PlanContent, _ *string) { c.Groups[0].Items[0].Assignee = "someone-else" },
		"item capabilities": func(c *PlanContent, _ *string) {
			c.Groups[0].Items[0].RequiredCapabilities = []string{"email"}
		},
		"item dependency":      func(c *PlanContent, _ *string) { c.Groups[0].Items[1].DependsOn = nil },
		"item expected result": func(c *PlanContent, _ *string) { c.Groups[0].Items[0].ExpectedResult = "changed" },
		"item priority":        func(c *PlanContent, _ *string) { c.Groups[0].Items[0].Priority = 9 },
		"validation required":  func(c *PlanContent, _ *string) { c.Validations[0].Required = false },
		"validation expectation": func(c *PlanContent, _ *string) {
			c.Validations[0].Expectation = "something else"
		},
		"execution mode": func(c *PlanContent, _ *string) { c.Execution.Mode = ExecutionAuto },
		"execution preconditions": func(c *PlanContent, _ *string) {
			c.Execution.Preconditions = []string{"safe_branch"}
		},
		"clarification answer": func(c *PlanContent, _ *string) { c.Clarifications[0].Answer = "Production" },
		"clarification required": func(c *PlanContent, _ *string) {
			c.Clarifications[0].Required = false
		},
		"added item": func(c *PlanContent, _ *string) {
			c.Groups[0].Items = append(c.Groups[0].Items, TaskItem{ID: "itm-3", Description: "New"})
		},
		"removed item": func(c *PlanContent, _ *string) {
			c.Groups[0].Items = c.Groups[0].Items[:1]
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			content := hashableContent()
			objective := "Objective"
			mutate(&content, &objective)
			if changed := mustHash(t, objective, content, PolicySnapshot{}); changed == base {
				t.Errorf("changing %s did not change the approval hash", name)
			}
		})
	}
}

// Reordering changes execution order, so it is approval-relevant (FR-36).
func TestReorderingChangesTheHash(t *testing.T) {
	base := mustHash(t, "Objective", hashableContent(), PolicySnapshot{})

	reorderedItems := hashableContent()
	reorderedItems.Groups[0].Items[0], reorderedItems.Groups[0].Items[1] =
		reorderedItems.Groups[0].Items[1], reorderedItems.Groups[0].Items[0]
	if mustHash(t, "Objective", reorderedItems, PolicySnapshot{}) == base {
		t.Error("reordering items did not change the approval hash")
	}

	reorderedGroups := hashableContent()
	reorderedGroups.Groups = append(reorderedGroups.Groups, TaskGroup{
		ID: "grp-2", Title: "Second", Items: []TaskItem{{ID: "itm-9", Description: "x"}},
	})
	withSwap := hashableContent()
	withSwap.Groups = []TaskGroup{
		{ID: "grp-2", Title: "Second", Items: []TaskItem{{ID: "itm-9", Description: "x"}}},
		reorderedGroups.Groups[0],
	}
	if mustHash(t, "Objective", reorderedGroups, PolicySnapshot{}) ==
		mustHash(t, "Objective", withSwap, PolicySnapshot{}) {
		t.Error("reordering groups did not change the approval hash")
	}
}

// The enforced policy a version was reviewed under is part of the decision:
// approving tasks with a branch gate is not the same as approving them without
// one (FR-144).
func TestPolicySnapshotIsApprovalRelevant(t *testing.T) {
	content := hashableContent()
	without := mustHash(t, "Objective", content, PolicySnapshot{})
	with := mustHash(t, "Objective", content, PolicySnapshot{
		Profile:  "software_project",
		Enforced: map[string]bool{"safe_branch": true},
	})
	if without == with {
		t.Error("the enforced policy snapshot did not affect the approval hash")
	}

	// A different enforcement decision is a different approval.
	off := mustHash(t, "Objective", content, PolicySnapshot{
		Profile:  "software_project",
		Enforced: map[string]bool{"safe_branch": false},
	})
	if with == off {
		t.Error("flipping an enforced control did not affect the approval hash")
	}
}

// The documented cosmetic exclusions, each with the reason it cannot change
// what happens (FR-34).
func TestCosmeticFieldsAreExcludedFromTheHash(t *testing.T) {
	base := mustHash(t, "Objective", hashableContent(), PolicySnapshot{})

	cases := map[string]func(*PlanContent){
		// Prose for the reader; nothing acts on it.
		"explanation": func(c *PlanContent) { c.Explanation = "Completely rewritten prose" },
		"rationale":   func(c *PlanContent) { c.Rationale = "A different justification" },
		"group notes": func(c *PlanContent) { c.Groups[0].Notes = "different notes" },
		// Display text for a reference; Ref is what anything acts on.
		"source title":   func(c *PlanContent) { c.Sources[0].Title = "Renamed" },
		"source excerpt": func(c *PlanContent) { c.Sources[0].Excerpt = "different excerpt" },
		// Labels; kind, path, and enabled decide the write.
		"artifact title":       func(c *PlanContent) { c.Artifacts[0].Title = "Renamed" },
		"artifact description": func(c *PlanContent) { c.Artifacts[0].Description = "different" },
		// How a question was presented, not what it asked or answered.
		"clarification detail":  func(c *PlanContent) { c.Clarifications[0].Detail = "different detail" },
		"clarification options": func(c *PlanContent) { c.Clarifications[0].Options = []string{"x"} },
		// Provenance, not content.
		"group author": func(c *PlanContent) { c.Groups[0].Author = AuthorUser },
		"item author":  func(c *PlanContent) { c.Groups[0].Items[0].Author = AuthorUser },
		"assumption author": func(c *PlanContent) {
			c.Assumptions[0].Author = AuthorUser
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			content := hashableContent()
			mutate(&content)
			if changed := mustHash(t, "Objective", content, PolicySnapshot{}); changed != base {
				t.Errorf("changing %s changed the approval hash; it is documented as cosmetic", name)
			}
		})
	}
}

// The canonical form is written out field by field so a new PlanContent field
// cannot silently join or skip the hash. This test is the reminder: if it
// fails, decide which side the new field belongs on and say so in canonical.go.
func TestCanonicalFormCoversTheExpectedFields(t *testing.T) {
	canonical := Canonicalize("Objective", hashableContent(), PolicySnapshot{})

	if canonical.Objective != "Objective" {
		t.Errorf("objective = %q", canonical.Objective)
	}
	if len(canonical.Groups) != 1 || len(canonical.Groups[0].Items) != 2 {
		t.Fatalf("groups did not canonicalize: %+v", canonical.Groups)
	}
	if canonical.Groups[0].Items[1].DependsOn[0] != "itm-1" {
		t.Errorf("dependencies did not canonicalize: %+v", canonical.Groups[0].Items[1])
	}
	if len(canonical.Clarifications) != 1 || canonical.Clarifications[0].Answer != "Staging" {
		t.Errorf("clarifications did not canonicalize: %+v", canonical.Clarifications)
	}
	if canonical.Execution.Mode != ExecutionStepThrough {
		t.Errorf("execution did not canonicalize: %+v", canonical.Execution)
	}
	if len(canonical.Artifacts) != 1 || !canonical.Artifacts[0].Enabled {
		t.Errorf("artifacts did not canonicalize: %+v", canonical.Artifacts)
	}
}
