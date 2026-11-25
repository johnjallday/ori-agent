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
	old := canonicalSurfaceFixture(t)
	if !bytes.Contains(old, []byte(`"ask_ori"`)) {
		t.Fatal("fixture no longer declares ask_ori; this test needs an older-release manifest")
	}
	if _, err := ParseSurfaceContribution(old); err != nil {
		t.Fatalf("an older release declaring host_intents.ask_ori must still mount: %v", err)
	}

	candidate := bytes.Replace(old, []byte(`"ask_ori": {
              "required_capabilities": []
            },
            `), nil, 1)
	if bytes.Contains(candidate, []byte(`"ask_ori"`)) {
		t.Fatal("could not remove ask_ori from the fixture; update the replacement to its current layout")
	}
	if _, err := ParseSurfaceContribution(candidate); err != nil {
		t.Fatalf("a candidate release without host_intents.ask_ori must mount: %v", err)
	}
}
