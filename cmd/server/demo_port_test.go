package main

import "testing"

func TestIsolatedDemoRefusesOccupiedPort(t *testing.T) {
	t.Setenv("ORI_DEMO_REQUIRE_FREE_PORT", "1")
	listener, err := reserveStartupListener("127.0.0.1:0", false)
	if err != nil {
		t.Fatal(err)
	}
	if listener == nil {
		t.Fatal("demo did not reserve its listener")
	}
	defer func() { _ = listener.Close() }()
	if second, err := reserveStartupListener(listener.Addr().String(), false); err == nil {
		if second != nil {
			_ = second.Close()
		}
		t.Fatal("an isolated demo accepted another listener's port")
	}
}
