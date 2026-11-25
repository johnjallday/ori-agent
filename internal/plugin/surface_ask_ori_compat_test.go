package plugin

import (
	"bytes"
	"testing"
)

// The Ask Ori host intent is being retired from plugin surfaces without a
// protocol or manifest-schema version change. Both manifest shapes therefore
// have to mount on this host: an older release that still declares
// host_intents.ask_ori, and a candidate release that has removed it. The
// strict decode rejects unknown keys, so removing the field from the host
// struct would break every installed older release; omitting it from a
// manifest must never be treated as a broken manifest.

func TestSurfaceContributionMountsWithAndWithoutAskOriIntent(t *testing.T) {
	// The canonical fixture is the current shape (reaper-plugin 0.12.1 and
	// later): no ask_ori declaration.
	current := canonicalSurfaceFixture(t)
	if bytes.Contains(current, []byte(`"ask_ori"`)) {
		t.Fatal("the canonical fixture declares ask_ori again; it mirrors reaper-plugin 0.12.1, which removed it")
	}
	if _, err := ParseSurfaceContribution(current); err != nil {
		t.Fatalf("a release without host_intents.ask_ori must mount: %v", err)
	}

	// An older installed release (reaper-plugin 0.12.0 and earlier) still
	// declares it.
	older := bytes.Replace(current, []byte(`"host_intents": {
`), []byte(`"host_intents": {
            "ask_ori": {
              "required_capabilities": []
            },
`), 1)
	if !bytes.Contains(older, []byte(`"ask_ori"`)) {
		t.Fatal("could not add ask_ori to the fixture; update the insertion to its current layout")
	}
	if _, err := ParseSurfaceContribution(older); err != nil {
		t.Fatalf("an older release declaring host_intents.ask_ori must still mount: %v", err)
	}
}
