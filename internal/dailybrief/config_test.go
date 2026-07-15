package dailybrief

import (
	"errors"
	"testing"
)

func TestNormalizeConfig_RequiresWorkspaceID(t *testing.T) {
	_, err := NormalizeConfig(Config{})
	if !errors.Is(err, ErrWorkspaceIDRequired) {
		t.Fatalf("expected ErrWorkspaceIDRequired, got %v", err)
	}
}

func TestNormalizeConfig_DefaultsEmptyFields(t *testing.T) {
	got, err := NormalizeConfig(Config{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if got.Timezone != "UTC" {
		t.Errorf("timezone = %q, want UTC", got.Timezone)
	}
	if got.ScheduleTime != "08:00" {
		t.Errorf("schedule time = %q, want 08:00", got.ScheduleTime)
	}
	if len(got.ScheduleDays) != 5 {
		t.Errorf("schedule days = %v, want 5 weekday defaults", got.ScheduleDays)
	}
	if got.Scope != ScopeAll {
		t.Errorf("scope = %q, want all", got.Scope)
	}
}

func TestNormalizeConfig_RejectsInvalidTimezone(t *testing.T) {
	_, err := NormalizeConfig(Config{WorkspaceID: "ws-1", Timezone: "Not/AZone"})
	if !errors.Is(err, ErrInvalidTimezone) {
		t.Fatalf("expected ErrInvalidTimezone, got %v", err)
	}
}

func TestNormalizeConfig_RejectsInvalidScheduleTime(t *testing.T) {
	for _, bad := range []string{"25:00", "8:00am", "not-a-time", "08:00:00"} {
		if _, err := NormalizeConfig(Config{WorkspaceID: "ws-1", ScheduleTime: bad}); !errors.Is(err, ErrInvalidScheduleTime) {
			t.Errorf("ScheduleTime %q: expected ErrInvalidScheduleTime, got %v", bad, err)
		}
	}
}

func TestNormalizeConfig_AcceptsValidScheduleTime(t *testing.T) {
	got, err := NormalizeConfig(Config{WorkspaceID: "ws-1", ScheduleTime: "23:59"})
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if got.ScheduleTime != "23:59" {
		t.Errorf("schedule time = %q, want 23:59", got.ScheduleTime)
	}
}

func TestNormalizeConfig_DeduplicatesAndLowercasesScheduleDays(t *testing.T) {
	got, err := NormalizeConfig(Config{WorkspaceID: "ws-1", ScheduleDays: []string{"Mon", " MON ", "tue", "bogus"}})
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if want := []string{"mon", "tue"}; !equalStringSlices(got.ScheduleDays, want) {
		t.Errorf("schedule days = %v, want %v", got.ScheduleDays, want)
	}
}

func TestNormalizeConfig_AllScopeDropsSelectedWorkspaceIDs(t *testing.T) {
	got, err := NormalizeConfig(Config{WorkspaceID: "ws-1", Scope: ScopeAll, SelectedWorkspaceIDs: []string{"ws-2"}})
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if got.SelectedWorkspaceIDs != nil {
		t.Errorf("expected selected workspace ids cleared for scope=all, got %v", got.SelectedWorkspaceIDs)
	}
}

func TestNormalizeConfig_SelectedScopeNormalizesIDs(t *testing.T) {
	got, err := NormalizeConfig(Config{
		WorkspaceID:          "ws-1",
		Scope:                ScopeSelected,
		SelectedWorkspaceIDs: []string{" ws-2 ", "ws-2", "", "ws-3"},
	})
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if want := []string{"ws-2", "ws-3"}; !equalStringSlices(got.SelectedWorkspaceIDs, want) {
		t.Errorf("selected workspace ids = %v, want %v", got.SelectedWorkspaceIDs, want)
	}
}

func TestNormalizeConfig_UnknownScopeDefaultsToAll(t *testing.T) {
	got, err := NormalizeConfig(Config{WorkspaceID: "ws-1", Scope: Scope("bogus")})
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if got.Scope != ScopeAll {
		t.Errorf("scope = %q, want all", got.Scope)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
