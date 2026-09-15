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

// Adding a shape must not change how any existing declaration normalizes. The
// digests were recorded from the build before the integration_install shape.
func TestExistingDeclarationsNormalizeByteIdentically(t *testing.T) {
	declarations := map[string]specialist.SetupJourney{}
	for _, quest := range hostquests.All() {
		declarations["host:"+quest.ID] = quest
	}
	for _, entry := range specialist.All() {
		if entry.SetupJourney != nil {
			declarations["specialist:"+entry.Slug] = *entry.SetupJourney
		}
	}
	want := map[string]string{
		"host:email_ops_setup":        "29e433af71b15096943c4c9af211b3db61c519221328ec0594f28c5b094e6542",
		"specialist:music_production": "a1dfb1f72bfb2e3188edcd8f3a2b93cda3276644786dfe5b8a1d88f6c7424ac1",
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
