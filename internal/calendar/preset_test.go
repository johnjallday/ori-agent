package calendar

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGoogleCalendarPreset_Shape(t *testing.T) {
	p := GoogleCalendarPreset()
	if p.ID != GoogleCalendarPresetID || p.ServerName != GoogleCalendarServerName {
		t.Fatalf("unexpected preset identity: %+v", p)
	}
	if p.Transport != StreamableHTTPTransport {
		t.Fatalf("preset transport = %q, want %q", p.Transport, StreamableHTTPTransport)
	}
	if p.URL != GoogleCalendarMCPURL {
		t.Fatalf("preset URL = %q, want the official endpoint", p.URL)
	}
	if !p.DeveloperPreview {
		t.Fatal("Google Calendar preset must be marked developer_preview")
	}
	if len(p.Prerequisites) == 0 {
		t.Fatal("preset must explain the Cloud project/API/consent/redirect prerequisites")
	}
	if !strings.HasPrefix(p.URL, "https://") {
		t.Fatalf("preset endpoint must be HTTPS, got %q", p.URL)
	}
}

// TestGoogleCalendarPreset_NoEmbeddedSecret is the FR2/3.4 guard: the shipped
// preset must never embed OAuth client credentials. Assert against the
// serialized form so a future field addition can't sneak a secret in.
func TestGoogleCalendarPreset_NoEmbeddedSecret(t *testing.T) {
	data, err := json.Marshal(GoogleCalendarPreset())
	if err != nil {
		t.Fatalf("marshal preset: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, banned := range []string{"client_secret", "clientsecret", "client_id", "clientid", "refresh_token", "access_token", "auth_ref"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("shipped preset must not contain %q; serialized form: %s", banned, data)
		}
	}
}

func TestFindPreset(t *testing.T) {
	if _, ok := FindPreset(GoogleCalendarPresetID); !ok {
		t.Fatal("expected to find the google-calendar preset")
	}
	if _, ok := FindPreset("does-not-exist"); ok {
		t.Fatal("expected no preset for an unknown id")
	}
}

func TestShippedPresets_AllCredentialFree(t *testing.T) {
	// Guard against credential-bearing KEYS, not the word "secret" in prose:
	// the prerequisite copy legitimately tells the user to have their own
	// "client secret" ready.
	bannedKeys := []string{"client_secret", "clientsecret", "client_id", "clientid", "refresh_token", "access_token", "auth_ref"}
	for _, p := range ShippedPresets() {
		data, _ := json.Marshal(p)
		lower := strings.ToLower(string(data))
		for _, banned := range bannedKeys {
			if strings.Contains(lower, banned) {
				t.Fatalf("preset %q serialized form contains credential key %q: %s", p.ID, banned, data)
			}
		}
	}
}
