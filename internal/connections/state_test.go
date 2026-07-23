package connections

import "testing"

func grant(p ProductKey, h GrantHealth) *ProductGrant {
	return &ProductGrant{ConnectionID: "conn-1", Product: p, Health: h}
}

func conn(subject string, grants ...*ProductGrant) *Connection {
	c := &Connection{ID: "conn-1", Provider: ProviderGoogle, Subject: subject, Grants: map[ProductKey]*ProductGrant{}}
	for _, g := range grants {
		c.Grants[g.Product] = g
	}
	return c
}

func TestDeriveState(t *testing.T) {
	cases := []struct {
		name  string
		setup func() *Connection
		want  ConnectionState
	}{
		{"nil", func() *Connection { return nil }, StateNotConnected},
		{"no subject idle", func() *Connection { return conn("") }, StateNotConnected},
		{"no subject connecting", func() *Connection { c := conn(""); c.Connecting = true; return c }, StateConnecting},
		{"bare identity", func() *Connection { return conn("sub-1") }, StateConnected},
		{"only not-enabled grant", func() *Connection { return conn("sub-1", grant(ProductGmail, HealthNotEnabled)) }, StateConnected},
		{"one healthy", func() *Connection { return conn("sub-1", grant(ProductGmail, HealthHealthy)) }, StateConnected},
		{"healthy + reconnect", func() *Connection {
			return conn("sub-1", grant(ProductGmail, HealthHealthy), grant(ProductCalendar, HealthReconnectRequired))
		}, StateNeedsAttention},
		{"healthy + connecting", func() *Connection {
			return conn("sub-1", grant(ProductGmail, HealthHealthy), grant(ProductDrive, HealthConnecting))
		}, StatePartiallyConnected},
		{"only connecting", func() *Connection { return conn("sub-1", grant(ProductDrive, HealthConnecting)) }, StateConnecting},
		{"advanced setup required", func() *Connection {
			return conn("sub-1", grant(ProductCalendar, HealthAdvancedSetupRequired))
		}, StateNeedsAttention},
		{"rate limited", func() *Connection { return conn("sub-1", grant(ProductDrive, HealthRateLimited)) }, StateNeedsAttention},
		{"disconnecting trumps healthy", func() *Connection {
			c := conn("sub-1", grant(ProductGmail, HealthHealthy))
			c.Disconnecting = true
			return c
		}, StateDisconnecting},
		{"identity re-handshake trumps healthy", func() *Connection {
			c := conn("sub-1", grant(ProductGmail, HealthHealthy))
			c.Connecting = true
			return c
		}, StateConnecting},
		{"attention trumps identity re-handshake", func() *Connection {
			c := conn("sub-1", grant(ProductGmail, HealthReconnectRequired))
			c.Connecting = true
			return c
		}, StateNeedsAttention},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveState(tc.setup()); got != tc.want {
				t.Fatalf("DeriveState = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGrantHealthClassification(t *testing.T) {
	attention := []GrantHealth{
		HealthScopeUpgradeRequired, HealthReconnectRequired, HealthAdvancedSetupRequired,
		HealthProviderUnavailable, HealthRateLimited, HealthAdminBlocked, HealthError,
	}
	for _, h := range attention {
		if !h.NeedsAttention() {
			t.Errorf("%q should NeedAttention", h)
		}
		if !h.IsEnabled() {
			t.Errorf("%q should be enabled", h)
		}
		if h.IsHealthy() {
			t.Errorf("%q should not be healthy", h)
		}
	}
	if !HealthHealthy.IsHealthy() || !HealthHealthy.IsEnabled() || HealthHealthy.NeedsAttention() {
		t.Error("Healthy classification wrong")
	}
	if HealthConnecting.NeedsAttention() {
		t.Error("Connecting must not NeedAttention (it is progress, not a problem)")
	}
	if HealthNotEnabled.IsEnabled() || GrantHealth("").IsEnabled() {
		t.Error("NotEnabled/empty must not be enabled")
	}
}
