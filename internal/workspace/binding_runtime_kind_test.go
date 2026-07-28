package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The classification table. Every row is a binding shape that exists in the
// wild — explicit kinds, the legacy email aliases that predate the field,
// ordinary MCP servers, and the malformed values that must fail closed.
func TestEffectiveRuntimeKind(t *testing.T) {
	cases := []struct {
		name       string
		binding    MCPBinding
		wantKind   BindingRuntimeKind
		wantErr    bool
		wantNative bool
		wantMCP    bool
	}{
		// Explicit kinds are honored as written.
		{
			name:       "explicit native email",
			binding:    MCPBinding{ID: "b1", ServerName: "gmail", RuntimeKind: RuntimeKindNativeEmail},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		{
			name:     "explicit mcp",
			binding:  MCPBinding{ID: "b2", ServerName: "filesystem", RuntimeKind: RuntimeKindMCP},
			wantKind: RuntimeKindMCP,
			wantMCP:  true,
		},
		// An explicit kind wins over the server name in BOTH directions: a real
		// MCP server merely named "email" stays MCP, and a native binding under an
		// unfamiliar name stays native.
		{
			name:     "explicit mcp on an email-sounding name",
			binding:  MCPBinding{ID: "b3", ServerName: "email", RuntimeKind: RuntimeKindMCP},
			wantKind: RuntimeKindMCP,
			wantMCP:  true,
		},
		{
			name:       "explicit native on an unfamiliar name",
			binding:    MCPBinding{ID: "b4", ServerName: "fastmail", RuntimeKind: RuntimeKindNativeEmail},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		// Legacy bindings carry no kind and are classified by name.
		{
			name:       "legacy gmail",
			binding:    MCPBinding{ID: "l1", ServerName: "gmail"},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		{
			name:       "legacy email",
			binding:    MCPBinding{ID: "l2", ServerName: "email"},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		{
			name:       "legacy microsoft-mail",
			binding:    MCPBinding{ID: "l3", ServerName: "microsoft-mail"},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		{
			name:       "legacy microsoft",
			binding:    MCPBinding{ID: "l4", ServerName: "microsoft"},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		{
			name:       "legacy outlook-mail",
			binding:    MCPBinding{ID: "l5", ServerName: "outlook-mail"},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		{
			name:       "legacy imap-smtp",
			binding:    MCPBinding{ID: "l6", ServerName: "imap-smtp"},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		{
			name:       "legacy imap_smtp",
			binding:    MCPBinding{ID: "l7", ServerName: "imap_smtp"},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		// Case and whitespace normalization.
		{
			name:       "mixed case and padding on the name",
			binding:    MCPBinding{ID: "n1", ServerName: "  GMail "},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		{
			name:       "mixed case and padding on the kind",
			binding:    MCPBinding{ID: "n2", ServerName: "whatever", RuntimeKind: BindingRuntimeKind(" Native_Email ")},
			wantKind:   RuntimeKindNativeEmail,
			wantNative: true,
		},
		// Ordinary MCP bindings are untouched.
		{
			name:     "filesystem stays mcp",
			binding:  MCPBinding{ID: "m1", ServerName: "filesystem"},
			wantKind: RuntimeKindMCP,
			wantMCP:  true,
		},
		{
			name:     "google calendar stays mcp",
			binding:  MCPBinding{ID: "m2", ServerName: "google-calendar"},
			wantKind: RuntimeKindMCP,
			wantMCP:  true,
		},
		{
			name:     "google drive stays mcp",
			binding:  MCPBinding{ID: "m3", ServerName: "google-drive"},
			wantKind: RuntimeKindMCP,
			wantMCP:  true,
		},
		{
			name:     "browser stays mcp",
			binding:  MCPBinding{ID: "m4", ServerName: "browser"},
			wantKind: RuntimeKindMCP,
			wantMCP:  true,
		},
		// A name that merely contains "mail" is not an alias.
		{
			name:     "mailchimp is not native email",
			binding:  MCPBinding{ID: "m5", ServerName: "mailchimp"},
			wantKind: RuntimeKindMCP,
			wantMCP:  true,
		},
		// Unknown explicit kinds fail closed: neither native nor MCP.
		{
			name:    "unknown explicit kind",
			binding: MCPBinding{ID: "u1", ServerName: "gmail", RuntimeKind: BindingRuntimeKind("native_calendar")},
			wantErr: true,
		},
		{
			name:    "typo in explicit kind",
			binding: MCPBinding{ID: "u2", ServerName: "filesystem", RuntimeKind: BindingRuntimeKind("mcpp")},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, err := tc.binding.EffectiveRuntimeKind()
			if tc.wantErr {
				if !errors.Is(err, ErrUnknownRuntimeKind) {
					t.Fatalf("err = %v, want ErrUnknownRuntimeKind", err)
				}
				if tc.binding.IsNativeEmail() || tc.binding.IsRuntimeMCP() {
					t.Fatal("an unclassifiable binding must be neither native nor MCP")
				}
				return
			}
			if err != nil {
				t.Fatalf("EffectiveRuntimeKind: %v", err)
			}
			if kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", kind, tc.wantKind)
			}
			if got := tc.binding.IsNativeEmail(); got != tc.wantNative {
				t.Fatalf("IsNativeEmail = %v, want %v", got, tc.wantNative)
			}
			if got := tc.binding.IsRuntimeMCP(); got != tc.wantMCP {
				t.Fatalf("IsRuntimeMCP = %v, want %v", got, tc.wantMCP)
			}
		})
	}
}

// The kind must survive a JSON round trip under the `runtime_kind` name, and a
// record written before the field existed must still decode (FR 21, 23).
func TestMCPBinding_RuntimeKindSerialization(t *testing.T) {
	binding := MCPBinding{ID: "b1", ServerName: "gmail", Enabled: true, RuntimeKind: RuntimeKindNativeEmail}
	data, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"runtime_kind":"native_email"`) {
		t.Fatalf("serialized form = %s, want a runtime_kind field", data)
	}

	var decoded MCPBinding
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.RuntimeKind != RuntimeKindNativeEmail {
		t.Fatalf("decoded kind = %q, want native_email", decoded.RuntimeKind)
	}

	// An MCP binding omits the field entirely rather than writing "mcp" into
	// every existing record.
	plain, err := json.Marshal(MCPBinding{ID: "b2", ServerName: "filesystem"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plain), "runtime_kind") {
		t.Fatalf("unset kind must be omitted: %s", plain)
	}

	// A pre-field record still classifies.
	var legacy MCPBinding
	if err := json.Unmarshal([]byte(`{"id":"old","server_name":"gmail","enabled":true}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if legacy.RuntimeKind != "" {
		t.Fatalf("legacy record gained a kind: %q", legacy.RuntimeKind)
	}
	if !legacy.IsNativeEmail() {
		t.Fatal("a legacy gmail binding must classify as native email")
	}
}

// Upsert must not quietly drop the kind — that would silently re-break the
// binding on the next save.
func TestUpsertMCPBinding_PreservesRuntimeKind(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	if err := ws.UpsertMCPBinding(MCPBinding{ID: "b1", ServerName: "gmail", Enabled: true, RuntimeKind: RuntimeKindNativeEmail}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	stored, ok := ws.GetMCPBinding("b1")
	if !ok || stored.RuntimeKind != RuntimeKindNativeEmail {
		t.Fatalf("stored binding = %+v, want native_email", stored)
	}

	// Relinking the same binding id keeps it native.
	if err := ws.UpsertMCPBinding(MCPBinding{ID: "b1", ServerName: "gmail", Enabled: true, RuntimeKind: RuntimeKindNativeEmail}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if bindings := ws.GetMCPBindings(); len(bindings) != 1 {
		t.Fatalf("relink created %d bindings, want 1", len(bindings))
	}
}

func TestValidateRuntimeKind(t *testing.T) {
	for _, ok := range []BindingRuntimeKind{"", RuntimeKindMCP, RuntimeKindNativeEmail, " MCP "} {
		if err := validateRuntimeKind(ok); err != nil {
			t.Fatalf("validateRuntimeKind(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []BindingRuntimeKind{"native_calendar", "server", "native"} {
		if err := validateRuntimeKind(bad); err == nil {
			t.Fatalf("validateRuntimeKind(%q) accepted an unknown kind", bad)
		}
	}
}
