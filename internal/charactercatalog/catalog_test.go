package charactercatalog

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

/* ---- the shipped catalog -------------------------------------------------- */

func TestEmbeddedCatalogLoads(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("embedded catalog failed to load: %v", err)
	}
	if c == nil {
		t.Fatal("expected a catalog")
	}
}

func TestEmbeddedCatalogShipsAtLeastEightWorkingCharacters(t *testing.T) {
	// FR-51: at least eight working choices *in addition to* Ori.
	working := MustLoad().Working()
	if len(working) < MinWorkingCharacters {
		t.Fatalf("expected >= %d working characters, got %d", MinWorkingCharacters, len(working))
	}
}

func TestEmbeddedCatalogHasExactlyOneGuideAndItIsOri(t *testing.T) {
	c := MustLoad()
	guide := c.Guide()
	if guide.Kind != KindGuide {
		t.Fatalf("reserved id %q did not resolve to a guide character", c.ReservedGuideID)
	}
	if guide.Name != "Ori" {
		t.Fatalf("expected the guide to be named Ori, got %q", guide.Name)
	}
	for _, ch := range c.Characters {
		if ch.Kind == KindGuide && ch.ID != c.ReservedGuideID {
			t.Fatalf("character %q is a second guide", ch.ID)
		}
	}
}

// The reserved identity is the whole basis of "a user's agent can never become
// Ori" (FR-19/FR-71), so assert it at the API a write path would actually call.
func TestGuideIdentityIsNotAssignableToAWorkingAgent(t *testing.T) {
	c := MustLoad()
	if c.IsAssignable(c.ReservedGuideID) {
		t.Fatal("the reserved guide id must never be assignable to a working agent")
	}
	for _, ch := range c.Working() {
		if !c.IsAssignable(ch.ID) {
			t.Errorf("working character %q should be assignable", ch.ID)
		}
	}
}

func TestUnknownIDIsNotAssignable(t *testing.T) {
	c := MustLoad()
	for _, id := range []CharacterID{"", "nope", "ori", "ori-guide-2", "../escape"} {
		if c.IsAssignable(id) {
			t.Errorf("id %q must not be assignable", id)
		}
	}
}

func TestWorkingOrderIsStable(t *testing.T) {
	c := MustLoad()
	first := c.Working()
	second := c.Working()
	if len(first) != len(second) {
		t.Fatal("Working() returned different lengths across calls")
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("Working() order is unstable at %d: %q vs %q", i, first[i].ID, second[i].ID)
		}
		if i > 0 && first[i-1].ID >= first[i].ID {
			t.Fatalf("Working() is not sorted at %d: %q >= %q", i, first[i-1].ID, first[i].ID)
		}
	}
}

// A character describes how an agent *looks and sounds*. If a prompt, tool, or
// permission field ever appears here, assigning a character would start granting
// capability — exactly what Non-Goal 6 and FR-73 forbid.
func TestCharacterCarriesNoCapabilityFields(t *testing.T) {
	raw, err := json.Marshal(MustLoad().Guide())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, forbidden := range []string{
		"prompt", "system_prompt", "tools", "skills", "toolbox",
		"permissions", "credentials", "model", "provider", "status", "level",
	} {
		if _, present := fields[forbidden]; present {
			t.Errorf("character exposes capability-bearing field %q", forbidden)
		}
	}
}

// Working characters are named for the role they depict, not with invented
// proper nouns.
//
// Invented names were dropped deliberately: a single common word is exactly
// where trademark collisions cluster, and a role reads more usefully next to
// the agent's own name anyway. This test keeps the policy from eroding one
// entry at a time (PRD FR-109, and the naming decision in
// docs/CHARACTER_ASSET_PROVENANCE.md).
func TestWorkingCharacterNamesAreDescriptiveRoles(t *testing.T) {
	for _, ch := range MustLoad().Working() {
		if !strings.Contains(ch.Name, " ") {
			t.Errorf("character %q is named %q — working characters use a descriptive "+
				"role such as \"Research Archivist\", not a single invented name", ch.ID, ch.Name)
		}
		// The id should read as the slug of the name, so the two cannot drift.
		slug := strings.ReplaceAll(strings.ToLower(ch.Name), " ", "-")
		if string(ch.ID) != slug {
			t.Errorf("character %q has id %q; expected %q to match its name", ch.Name, ch.ID, slug)
		}
	}
}

// Ori is the one proper noun in the catalog, and it is the product's own name
// rather than an invented character name.
func TestOnlyTheGuideUsesAProperName(t *testing.T) {
	c := MustLoad()
	if c.Guide().Name != "Ori" {
		t.Fatalf("expected the guide to be Ori, got %q", c.Guide().Name)
	}
	for _, ch := range c.Working() {
		if ch.Name == "Ori" {
			t.Errorf("working character %q must not be called Ori", ch.ID)
		}
	}
}

func TestEveryShippedCharacterLinksAProvenanceRecord(t *testing.T) {
	for _, ch := range MustLoad().Characters {
		if !strings.Contains(ch.Provenance, "#") {
			t.Errorf("character %q has no provenance anchor", ch.ID)
		}
		if !strings.HasPrefix(ch.Provenance, "docs/") {
			t.Errorf("character %q provenance %q should point into docs/", ch.ID, ch.Provenance)
		}
	}
}

func TestEveryShippedCharacterDeclaresAllThreeAssets(t *testing.T) {
	for _, ch := range MustLoad().Characters {
		if ch.Assets.Portrait == "" || ch.Assets.Sprite == "" || ch.Assets.Static == "" {
			t.Errorf("character %q is missing an asset variant: %+v", ch.ID, ch.Assets)
		}
	}
}

/* ---- validation ----------------------------------------------------------- */

// base is a minimal manifest that passes validation; each test below breaks
// exactly one thing so a failure names the rule that caught it.
func base(t *testing.T) map[string]any {
	t.Helper()
	char := func(id, kind string) map[string]any {
		return map[string]any{
			"id":             id,
			"entry_version":  1,
			"kind":           kind,
			"name":           strings.ToUpper(id[:1]) + id[1:],
			"family":         "resident",
			"family_label":   "Resident",
			"purpose":        "Finds sources.",
			"description":    "A resident holding a ledger.",
			"silhouette":     "Wide brow",
			"signature_prop": "Ledger",
			"idle_behavior":  "Straightens notes",
			"palette": map[string]any{
				"base": "#4f744a", "accent": "#4f744a", "ink": "#0f1a0e",
			},
			"assets": map[string]any{
				"portrait": "characters/" + id + "/portrait.svg",
				"sprite":   "characters/" + id + "/sprite.svg",
				"static":   "characters/" + id + "/static.svg",
			},
			"provenance": "docs/CHARACTER_ASSET_PROVENANCE.md#" + id,
		}
	}
	chars := []any{char("ori-guide", "guide")}
	for _, id := range []string{"aa", "bb", "cc", "dd", "ee", "ff", "gg", "hh"} {
		chars = append(chars, char(id, "working"))
	}
	return map[string]any{
		"catalog_version":   "1.0.0",
		"reserved_guide_id": "ori-guide",
		"characters":        chars,
	}
}

func parseMap(t *testing.T, m map[string]any) (*Catalog, error) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return parse(raw)
}

func TestBaseFixtureIsValid(t *testing.T) {
	if _, err := parseMap(t, base(t)); err != nil {
		t.Fatalf("base fixture should validate, got: %v", err)
	}
}

func TestRejectsDuplicateIDs(t *testing.T) {
	m := base(t)
	chars := m["characters"].([]any)
	dup := chars[1].(map[string]any)
	clone := map[string]any{}
	maps.Copy(clone, dup)
	m["characters"] = append(chars, clone)

	_, err := parseMap(t, m)
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("expected a duplicate-id error, got: %v", err)
	}
}

func TestRejectsWorkingCharacterClaimingTheReservedGuideID(t *testing.T) {
	m := base(t)
	chars := m["characters"].([]any)
	// A manifest that tries to hand Ori's identity to an assignable character.
	chars[1].(map[string]any)["id"] = "ori-guide"
	chars[1].(map[string]any)["kind"] = "working"

	_, err := parseMap(t, m)
	if err == nil {
		t.Fatal("expected the reserved guide id to be rejected for a working character")
	}
}

func TestRejectsMissingRequiredFields(t *testing.T) {
	for _, field := range []string{
		"name", "family", "family_label", "purpose",
		"description", "silhouette", "signature_prop", "idle_behavior",
		"provenance",
	} {
		t.Run(field, func(t *testing.T) {
			m := base(t)
			m["characters"].([]any)[1].(map[string]any)[field] = ""
			_, err := parseMap(t, m)
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("expected %q to be required, got: %v", field, err)
			}
		})
	}
}

func TestRejectsMissingAccessibleDescription(t *testing.T) {
	m := base(t)
	m["characters"].([]any)[1].(map[string]any)["description"] = "   "
	_, err := parseMap(t, m)
	if err == nil {
		t.Fatal("a whitespace-only accessible description must be rejected")
	}
}

// The tone layer is gone as a concept, so a manifest that reintroduces it must
// fail to load rather than have the fields quietly trimmed. The decoder's
// unknown-field rejection is what enforces that, which is why this asserts on
// loading rather than on the projection (PRD FR-22).
func TestRejectsTonePropertiesEntirely(t *testing.T) {
	for _, field := range []string{"tone_traits", "sample_line"} {
		t.Run(field, func(t *testing.T) {
			m := base(t)
			m["characters"].([]any)[1].(map[string]any)[field] = []string{"anything"}
			_, err := parseMap(t, m)
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("expected %q to be rejected outright, got: %v", field, err)
			}
		})
	}
}

// A working entry still has to carry reviewed visual and provenance metadata:
// removing tone must not have loosened what makes an asset shippable (FR-22).
func TestVisualAndProvenanceMetadataIsStillRequired(t *testing.T) {
	cat := MustLoad()
	for _, ch := range cat.Working() {
		if strings.TrimSpace(ch.Description) == "" {
			t.Errorf("%s: accessible description is missing", ch.ID)
		}
		if strings.TrimSpace(ch.Assets.Portrait) == "" {
			t.Errorf("%s: portrait asset is missing", ch.ID)
		}
		if !strings.Contains(ch.Provenance, "#") {
			t.Errorf("%s: provenance must link to a specific record", ch.ID)
		}
	}
	// The catalog must still offer a usable working set after the removal.
	if len(cat.Working()) < MinWorkingCharacters {
		t.Fatalf("working set shrank to %d, below the floor of %d", len(cat.Working()), MinWorkingCharacters)
	}
}

func TestRejectsProvenanceWithoutAnchor(t *testing.T) {
	m := base(t)
	m["characters"].([]any)[1].(map[string]any)["provenance"] = "docs/CHARACTER_ASSET_PROVENANCE.md"
	_, err := parseMap(t, m)
	if err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Fatalf("expected an anchor requirement, got: %v", err)
	}
}

func TestRejectsUnsafeAssetPaths(t *testing.T) {
	cases := map[string]string{
		"absolute":       "/etc/passwd.svg",
		"traversal":      "characters/../../secret.svg",
		"remote":         "https://example.com/x.svg",
		"protocol-less":  "//example.com/x.svg",
		"backslash":      `characters\ori\portrait.svg`,
		"outside-tree":   "images/ori/portrait.svg",
		"wrong-type":     "characters/ori/portrait.png",
		"empty":          "",
		"empty-segment":  "characters//portrait.svg",
		"dot-segment":    "characters/./portrait.svg",
		"parent-segment": "characters/ori/../portrait.svg",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			m := base(t)
			m["characters"].([]any)[1].(map[string]any)["assets"].(map[string]any)["portrait"] = path
			if _, err := parseMap(t, m); err == nil {
				t.Fatalf("expected asset path %q to be rejected", path)
			}
		})
	}
}

func TestRejectsNonHexPaletteTokens(t *testing.T) {
	for _, bad := range []string{"red", "#fff", "#12345g", "rgb(0,0,0)", "", "#1234567"} {
		t.Run(bad, func(t *testing.T) {
			m := base(t)
			m["characters"].([]any)[1].(map[string]any)["palette"].(map[string]any)["base"] = bad
			if _, err := parseMap(t, m); err == nil {
				t.Fatalf("expected palette value %q to be rejected", bad)
			}
		})
	}
}

func TestRejectsUnsupportedCatalogVersion(t *testing.T) {
	for _, v := range []string{"", "2.0.0", "0.9.0", "banana", "1"} {
		t.Run(v, func(t *testing.T) {
			m := base(t)
			m["catalog_version"] = v
			if _, err := parseMap(t, m); err == nil {
				t.Fatalf("expected catalog_version %q to be rejected", v)
			}
		})
	}
}

func TestRejectsIllegalIDs(t *testing.T) {
	for _, id := range []string{"", "Ori", "ori guide", "ori_guide", "-ori", "ori-", "ori--guide", "ori/guide", "../ori"} {
		t.Run(id, func(t *testing.T) {
			m := base(t)
			m["characters"].([]any)[1].(map[string]any)["id"] = id
			if _, err := parseMap(t, m); err == nil {
				t.Fatalf("expected id %q to be rejected", id)
			}
		})
	}
}

func TestRejectsMissingGuide(t *testing.T) {
	m := base(t)
	chars := m["characters"].([]any)
	m["characters"] = chars[1:] // drop the guide
	_, err := parseMap(t, m)
	if err == nil || !strings.Contains(err.Error(), "guide") {
		t.Fatalf("expected a missing-guide error, got: %v", err)
	}
}

func TestRejectsTooFewWorkingCharacters(t *testing.T) {
	m := base(t)
	chars := m["characters"].([]any)
	m["characters"] = chars[:3] // guide + 2 working
	_, err := parseMap(t, m)
	if err == nil || !strings.Contains(err.Error(), "working") {
		t.Fatalf("expected a working-character floor error, got: %v", err)
	}
}

func TestRejectsUnknownKind(t *testing.T) {
	m := base(t)
	m["characters"].([]any)[1].(map[string]any)["kind"] = "npc"
	_, err := parseMap(t, m)
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("expected an unknown-kind error, got: %v", err)
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	m := base(t)
	m["characters"].([]any)[1].(map[string]any)["grants_tool"] = "shell"
	if _, err := parseMap(t, m); err == nil {
		t.Fatal("expected an unknown field to be rejected as schema drift")
	}
}

func TestRejectsZeroEntryVersion(t *testing.T) {
	m := base(t)
	m["characters"].([]any)[1].(map[string]any)["entry_version"] = 0
	if _, err := parseMap(t, m); err == nil {
		t.Fatal("expected entry_version 0 to be rejected")
	}
}

func TestRejectsUnknownRole(t *testing.T) {
	m := base(t)
	// A typo would silently stop a character from ever being recommended —
	// exactly the kind of failure that goes unnoticed in production.
	m["characters"].([]any)[1].(map[string]any)["roles"] = []any{"resercher"}
	_, err := parseMap(t, m)
	if err == nil || !strings.Contains(err.Error(), "roles") {
		t.Fatalf("expected an unknown-role error, got: %v", err)
	}
}

func TestRejectsCLIAgentRoleAffinity(t *testing.T) {
	m := base(t)
	// Built-in CLI agents are read-only and can never be assigned a character,
	// so an affinity for them could never be acted on (FR-70).
	m["characters"].([]any)[1].(map[string]any)["roles"] = []any{"cli_agent"}
	if _, err := parseMap(t, m); err == nil {
		t.Fatal("expected cli_agent to be rejected as an unassignable role")
	}
}

func TestRejectsDuplicateRole(t *testing.T) {
	m := base(t)
	m["characters"].([]any)[1].(map[string]any)["roles"] = []any{"researcher", "researcher"}
	if _, err := parseMap(t, m); err == nil {
		t.Fatal("expected a duplicate role to be rejected")
	}
}

// Roles are an ordering hint, so an entry without them must still load. Nothing
// downstream may treat "no roles" as "not selectable" (FR-65).
func TestRolesAreOptional(t *testing.T) {
	m := base(t)
	delete(m["characters"].([]any)[1].(map[string]any), "roles")
	if _, err := parseMap(t, m); err != nil {
		t.Fatalf("roles must be optional, got: %v", err)
	}
}

// Every assignable role should have at least one character that suits it,
// otherwise the recommendation silently degrades to "first unused" for agents
// in that role and nobody finds out.
func TestEveryAssignableRoleHasACharacter(t *testing.T) {
	c := MustLoad()
	covered := map[types.AgentRole]bool{}
	for _, ch := range c.Working() {
		for _, r := range ch.Roles {
			covered[r] = true
		}
	}
	// RoleGeneral is deliberately uncovered: "general" states no preference, so
	// pinning characters to it would make one of them the default for every
	// unspecialised agent.
	for _, r := range []types.AgentRole{
		types.RoleOrchestrator,
		types.RoleResearcher,
		types.RoleAnalyzer,
		types.RoleSynthesizer,
		types.RoleValidator,
		types.RoleSpecialist,
	} {
		if !covered[r] {
			t.Errorf("no catalog character declares an affinity for role %q", r)
		}
	}
}

func TestGetReportsMissingCharacters(t *testing.T) {
	c := MustLoad()
	if _, ok := c.Get("does-not-exist"); ok {
		t.Fatal("Get must report a miss for an unknown id")
	}
	if ch, ok := c.Get(c.ReservedGuideID); !ok || ch.Name != "Ori" {
		t.Fatalf("Get should resolve the reserved guide id, got %+v ok=%v", ch, ok)
	}
}
