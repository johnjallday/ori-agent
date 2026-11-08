package sessionhttp

import "testing"

// PRD #792 FR10: the upgrade review names an added words section in the
// package's own plural, lower case, and promises nothing else.
func TestProviderUpgradeProjectCardAdditionLine(t *testing.T) {
	if got := providerUpgradeProjectCardAddition("Songs"); got != "Adds the package's words for the songs card and roster. Nothing else changes." {
		t.Fatalf("line = %q", got)
	}
	if got := providerUpgradeProjectCardAddition("  "); got != "Adds the package's words for the projects card and roster. Nothing else changes." {
		t.Fatalf("blank plural = %q", got)
	}
}
