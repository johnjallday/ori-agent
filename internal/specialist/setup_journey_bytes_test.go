package specialist_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/johnjallday/ori-agent/internal/hostquests"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// normalizedDigest is the SHA-256 of a declaration's normalized JSON encoding.
func normalizedDigest(t *testing.T, declaration specialist.SetupJourney) string {
	t.Helper()
	normalized, err := specialist.NormalizeSetupJourney(declaration)
	if err != nil {
		t.Fatalf("normalize %q: %v", declaration.ID, err)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// FR 5: changing the compiled shapes must not change how an existing host
// declaration normalizes. The digest was recorded from the build before this
// feature added the integration_install and project_setup shapes, and
// re-recorded when the summary step's copy said "ticket" instead of "task"
// (one-vocabulary): a copy change, not a shape change.
func TestExistingDeclarationsNormalizeByteIdentically(t *testing.T) {
	declarations := map[string]specialist.SetupJourney{}
	for _, quest := range hostquests.All() {
		declarations["host:"+quest.ID] = quest
	}
	want := map[string]string{
		"host:email_ops_setup": "c6b713cf80c1797ef3a854f1f2ccadf8553559ecb4c27b70bcc8a7496d48c9f3",
	}
	for name, declaration := range declarations {
		got := normalizedDigest(t, declaration)
		if want[name] != got {
			t.Errorf("%s normalized digest = %s, want %s", name, got, want[name])
		}
	}
	if len(declarations) != len(want) {
		t.Errorf("checked %d declarations, want %d", len(declarations), len(want))
	}
}
