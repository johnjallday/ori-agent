package server

import "testing"

func TestGoogleProductEnabled_DefaultsOnAndTogglesOff(t *testing.T) {
	t.Setenv("ORI_GOOGLE_DRIVE_ENABLED", "")
	if !googleProductEnabled("DRIVE") {
		t.Error("a product must default to enabled when the flag is unset")
	}
	for _, v := range []string{"0", "false", "no", "OFF"} {
		t.Setenv("ORI_GOOGLE_DRIVE_ENABLED", v)
		if googleProductEnabled("DRIVE") {
			t.Errorf("ORI_GOOGLE_DRIVE_ENABLED=%q must hard-disable", v)
		}
	}
	for _, v := range []string{"1", "true", "on", "yes"} {
		t.Setenv("ORI_GOOGLE_DRIVE_ENABLED", v)
		if !googleProductEnabled("DRIVE") {
			t.Errorf("ORI_GOOGLE_DRIVE_ENABLED=%q must keep it enabled", v)
		}
	}
}

func TestMCPToolExposureAllowed_DriveFailClosedAndFlag(t *testing.T) {
	b := &ServerBuilder{}
	const driveURL = "https://drivemcp.googleapis.com/mcp/v1"
	const otherURL = "https://example.com/mcp"

	t.Setenv("ORI_GOOGLE_DRIVE_ENABLED", "1")
	if !b.mcpToolExposureAllowed(driveURL, "read_file_content") {
		t.Error("allowlisted Drive tool must be permitted when enabled")
	}
	if b.mcpToolExposureAllowed(driveURL, "create_file") {
		t.Error("mutating Drive tool must be denied (fail-closed)")
	}
	if b.mcpToolExposureAllowed(driveURL, "some_unknown_tool") {
		t.Error("unknown Drive tool must be denied (fail-closed)")
	}
	if !b.mcpToolExposureAllowed(otherURL, "create_file") {
		t.Error("a non-Google server must be unrestricted by this policy")
	}

	// Independent hard-disable denies even allowlisted Drive tools.
	t.Setenv("ORI_GOOGLE_DRIVE_ENABLED", "0")
	if b.mcpToolExposureAllowed(driveURL, "read_file_content") {
		t.Error("hard-disabled Drive must deny all of its tools (FR 75)")
	}

	// Calendar flag is independent: disabling Drive must not disable Calendar.
	const calURL = "https://calendarmcp.googleapis.com/mcp/v1"
	t.Setenv("ORI_GOOGLE_CALENDAR_ENABLED", "1")
	if !b.mcpToolExposureAllowed(calURL, "list_events") {
		t.Error("Calendar must stay enabled when only Drive is disabled")
	}
	t.Setenv("ORI_GOOGLE_CALENDAR_ENABLED", "0")
	if b.mcpToolExposureAllowed(calURL, "list_events") {
		t.Error("Calendar hard-disable must deny its tools")
	}
}
