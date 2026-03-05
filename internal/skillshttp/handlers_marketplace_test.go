package skillshttp

import "testing"

func TestIsValidMarketplacePackageSpec(t *testing.T) {
	tests := []struct {
		name  string
		spec  string
		valid bool
	}{
		{name: "valid basic", spec: "vercel-labs/skills@find-skills", valid: true},
		{name: "valid dotted owner", spec: "mcp-hub.momenta.works/finder@find-skills-ai", valid: true},
		{name: "invalid missing skill", spec: "vercel-labs/skills", valid: false},
		{name: "invalid spaces", spec: "vercel labs/skills@find-skills", valid: false},
		{name: "invalid extra text", spec: "vercel-labs/skills@find-skills --agent universal", valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isValidMarketplacePackageSpec(tc.spec)
			if got != tc.valid {
				t.Fatalf("isValidMarketplacePackageSpec(%q) = %v, want %v", tc.spec, got, tc.valid)
			}
		})
	}
}

func TestSanitizeMarketplaceQuery(t *testing.T) {
	raw := "   find    skills   for  ui   "
	got := sanitizeMarketplaceQuery(raw)
	if got != "find skills for ui" {
		t.Fatalf("sanitizeMarketplaceQuery() = %q", got)
	}
}

func TestParseSkillsFindOutput(t *testing.T) {
	output := "\x1b[38;5;102mInstall with\x1b[0m npx skills add <owner/repo@skill>\n\n" +
		"\x1b[38;5;145mvercel-labs/skills@find-skills\x1b[0m \x1b[36m418K installs\x1b[0m\n" +
		"\x1b[38;5;102m└ https://skills.sh/vercel-labs/skills/find-skills\x1b[0m\n\n" +
		"\x1b[38;5;145mmcp-hub.momenta.works/finder@find-skills-ai\x1b[0m \x1b[36m74 installs\x1b[0m\n" +
		"\x1b[38;5;102m└ https://skills.sh/mcp-hub.momenta.works/finder/find-skills-ai\x1b[0m\n"

	results := parseSkillsFindOutput(output, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Package != "vercel-labs/skills@find-skills" {
		t.Fatalf("unexpected package[0]: %q", results[0].Package)
	}
	if results[0].Repository != "vercel-labs/skills" {
		t.Fatalf("unexpected repository[0]: %q", results[0].Repository)
	}
	if results[0].Skill != "find-skills" {
		t.Fatalf("unexpected skill[0]: %q", results[0].Skill)
	}
	if results[0].Installs != "418K installs" {
		t.Fatalf("unexpected installs[0]: %q", results[0].Installs)
	}
	if results[0].URL != "https://skills.sh/vercel-labs/skills/find-skills" {
		t.Fatalf("unexpected url[0]: %q", results[0].URL)
	}

	if results[1].Package != "mcp-hub.momenta.works/finder@find-skills-ai" {
		t.Fatalf("unexpected package[1]: %q", results[1].Package)
	}
	if results[1].Skill != "find-skills-ai" {
		t.Fatalf("unexpected skill[1]: %q", results[1].Skill)
	}
}
