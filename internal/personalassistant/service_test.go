package personalassistant

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
)

type fakeEligibility struct {
	eligible bool
	version  int
}

func (f fakeEligibility) IsPersonalAssistantEligible() bool        { return f.eligible }
func (f fakeEligibility) PersonalAssistantEligibilityVersion() int { return f.version }

type readTrackingStore struct {
	state       *State
	err         error
	reads       int
	mutationHit bool
}

func (s *readTrackingStore) CreateState(context.Context, *State) (*State, error) {
	s.mutationHit = true
	return nil, errors.New("unexpected mutation")
}
func (s *readTrackingStore) GetState(context.Context, string) (*State, error) {
	s.reads++
	return s.state.Clone(), s.err
}
func (s *readTrackingStore) UpdateState(context.Context, *State, int64) (*State, error) {
	s.mutationHit = true
	return nil, errors.New("unexpected mutation")
}
func (s *readTrackingStore) CreateAssignment(context.Context, *Assignment) (*Assignment, error) {
	s.mutationHit = true
	return nil, errors.New("unexpected mutation")
}
func (s *readTrackingStore) GetAssignment(context.Context, string, string) (*Assignment, error) {
	return nil, ErrNotFound
}
func (s *readTrackingStore) UpdateAssignment(context.Context, *Assignment, int64) (*Assignment, error) {
	s.mutationHit = true
	return nil, errors.New("unexpected mutation")
}

type fakeHQReader struct {
	status *personalhq.Status
	err    error
	reads  int
}

func (f *fakeHQReader) Status(context.Context, string) (*personalhq.Status, error) {
	f.reads++
	return f.status, f.err
}

type fakeBriefReader struct {
	config *dailybrief.Config
	err    error
	reads  int
}

func (f *fakeBriefReader) GetConfig(context.Context, string) (*dailybrief.Config, error) {
	f.reads++
	if f.config == nil {
		return nil, f.err
	}
	copyConfig := *f.config
	copyConfig.ScheduleDays = append([]string(nil), f.config.ScheduleDays...)
	return &copyConfig, f.err
}

type fakeModelReader struct{ availability SourceAvailability }

func (f fakeModelReader) PersonalAssistantModelAvailability() SourceAvailability {
	return f.availability
}

func serviceMatrixFixture(status RelationshipStatus) (*Service, *readTrackingStore, *fakeHQReader, *fakeBriefReader, *session.Workspace) {
	state := activeTestState("local", "assistant-a")
	state.Status = status
	state.StateVersion = 7
	workspace := &session.Workspace{
		ID: "hq-local", OwnerUserID: "local",
		AgentInstances: []session.AgentInstance{{
			ID: "instance-local", Name: "Ada", EntryPoint: true,
			Role: "orchestrator", CustomInstructions: "Keep scope bounded.",
		}},
	}
	store := &readTrackingStore{state: state}
	hq := &fakeHQReader{status: &personalhq.Status{
		UserID: "local", WorkspaceID: workspace.ID, Workspace: workspace, Valid: true,
	}}
	briefs := &fakeBriefReader{config: &dailybrief.Config{
		WorkspaceID: workspace.ID, UserID: "local", Timezone: "America/New_York",
		ScheduleDays: []string{"mon", "tue"}, ScheduleTime: "08:00",
		ScheduleEnabled: true, ConfigRevision: 3,
	}}
	service := NewService(
		fakeEligibility{eligible: true, version: CurrentRolloutVersion},
		store, hq, briefs,
		fakeModelReader{availability: SourceAvailability{Available: true, Status: AvailabilityAvailable}},
	)
	return service, store, hq, briefs, workspace
}

func TestServiceGet_StateMatrix(t *testing.T) {
	t.Run("eligible no record", func(t *testing.T) {
		store := &readTrackingStore{err: ErrNotFound}
		service := NewService(
			fakeEligibility{eligible: true, version: CurrentRolloutVersion}, store, nil, nil,
			fakeModelReader{availability: SourceAvailability{Status: AvailabilityNotConfigured, Reason: "model_not_configured"}},
		)
		projection, err := service.Get(context.Background(), "local")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if projection.State != APIStateNeedsHire || projection.NextAction != "hire" || projection.StateVersion != 0 {
			t.Fatalf("projection = %#v", projection)
		}
		if projection.Availability.Model.Status != AvailabilityNotConfigured {
			t.Fatalf("model availability = %#v", projection.Availability.Model)
		}
	})

	for _, test := range []struct {
		status RelationshipStatus
		want   APIState
		action string
	}{
		{StatusActive, APIStateActive, "ask"},
		{StatusPaused, APIStatePaused, "resume"},
	} {
		t.Run(string(test.status), func(t *testing.T) {
			service, store, _, _, workspace := serviceMatrixFixture(test.status)
			before := *workspace
			before.AgentInstances = append([]session.AgentInstance(nil), workspace.AgentInstances...)
			projection, err := service.Get(context.Background(), "local")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if projection.State != test.want || projection.NextAction != test.action || projection.StateVersion != 7 {
				t.Fatalf("projection = %#v", projection)
			}
			if projection.AssistantID != "assistant-a" || projection.HQAgentInstanceID != "instance-local" || projection.Mandate == "" {
				t.Fatalf("canonical identity/working agreement missing: %#v", projection)
			}
			if projection.DailyBrief == nil || projection.DailyBrief.ConfigRevision != 3 {
				t.Fatalf("daily brief = %#v", projection.DailyBrief)
			}
			if store.mutationHit || !reflect.DeepEqual(before, *workspace) {
				t.Fatal("GET mutated relationship, tools, permissions, or workspace membership")
			}
		})
	}

	t.Run("hiring", func(t *testing.T) {
		service, _, _, _, _ := serviceMatrixFixture(StatusHiring)
		projection, err := service.Get(context.Background(), "local")
		if err != nil || projection.State != APIStateHiring || projection.NextAction != "resume_hire" {
			t.Fatalf("projection=%#v err=%v", projection, err)
		}
	})
}

func TestServiceGet_IneligibleNeverReadsRelationshipOrLeaksAgreement(t *testing.T) {
	store := &readTrackingStore{state: activeTestState("local", "assistant-secret")}
	service := NewService(fakeEligibility{eligible: false}, store, nil, nil, nil)
	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateIneligible || store.reads != 0 {
		t.Fatalf("state=%s store reads=%d", projection.State, store.reads)
	}
	if projection.AssistantID != "" || projection.Mandate != "" || projection.HQWorkspaceID != "" {
		t.Fatalf("ineligible projection leaked relationship data: %#v", projection)
	}
}

func TestServiceGet_KillSwitchHidesButDoesNotBreakActiveBinding(t *testing.T) {
	_, store, hq, briefs, _ := serviceMatrixFixture(StatusActive)
	disabled := NewService(fakeEligibility{eligible: false, version: CurrentRolloutVersion}, store, hq, briefs, nil)
	hidden, err := disabled.Get(context.Background(), "local")
	if err != nil || hidden.State != APIStateIneligible || store.mutationHit {
		t.Fatalf("disabled projection=%#v err=%v mutation=%v", hidden, err, store.mutationHit)
	}
	reenabled := NewService(
		fakeEligibility{eligible: true, version: CurrentRolloutVersion}, store, hq, briefs,
		fakeModelReader{availability: SourceAvailability{Available: true, Status: AvailabilityAvailable}},
	)
	restored, err := reenabled.Get(context.Background(), "local")
	if err != nil || restored.State != APIStateActive || restored.AssistantID != "assistant-a" || restored.HQWorkspaceID != "hq-local" {
		t.Fatalf("re-enabled projection=%#v err=%v", restored, err)
	}
}

func TestServiceGet_InvalidLinksFailClosedWithSourceAvailability(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeHQReader, *session.Workspace)
		status AvailabilityStatus
		reason string
	}{
		{
			name: "missing HQ",
			mutate: func(hq *fakeHQReader, _ *session.Workspace) {
				hq.status.Valid = false
				hq.status.Workspace = nil
			},
			status: AvailabilityUnavailable, reason: "link_mismatch",
		},
		{
			name: "missing agent",
			mutate: func(_ *fakeHQReader, workspace *session.Workspace) {
				workspace.AgentInstances = nil
			},
			status: AvailabilityUnavailable, reason: "instance_missing",
		},
		{
			name: "dependency failure",
			mutate: func(hq *fakeHQReader, _ *session.Workspace) {
				hq.err = errors.New("database unavailable")
			},
			status: AvailabilityDependencyError, reason: "read_failed",
		},
		{
			name: "cross-user linkage",
			mutate: func(hq *fakeHQReader, workspace *session.Workspace) {
				workspace.ID = "foreign-hq"
				hq.status.WorkspaceID = "foreign-hq"
			},
			status: AvailabilityUnavailable, reason: "link_mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _, hq, _, workspace := serviceMatrixFixture(StatusActive)
			test.mutate(hq, workspace)
			projection, err := service.Get(context.Background(), "local")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if projection.State != APIStateRepairNeeded || projection.Mandate != "" || projection.FocusAreas != nil || projection.HQWorkspaceID != "" {
				t.Fatalf("repair projection leaked invalid-link data: %#v", projection)
			}
			availability := projection.Availability.PersonalHQ
			if test.name == "missing agent" {
				availability = projection.Availability.AgentInstance
			}
			if availability.Status != test.status || availability.Reason != test.reason {
				t.Fatalf("availability = %#v", availability)
			}
		})
	}
}

func TestServiceGet_ModelAndBriefFailuresRemainIndependentFlags(t *testing.T) {
	service, _, _, briefs, _ := serviceMatrixFixture(StatusActive)
	briefs.config = nil
	briefs.err = errors.New("brief store offline")
	service.models = fakeModelReader{availability: SourceAvailability{Status: AvailabilityUnavailable, Reason: "configured_model_unavailable"}}

	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.State != APIStateActive {
		t.Fatalf("dependency capability changed relationship state: %s", projection.State)
	}
	if projection.Availability.DailyBrief.Status != AvailabilityDependencyError || projection.Availability.Model.Status != AvailabilityUnavailable {
		t.Fatalf("availability = %#v", projection.Availability)
	}
	if projection.DailyBrief != nil {
		t.Fatal("failed brief source fabricated a config")
	}
}

func TestServiceGet_DailyBriefOwnershipMismatchDoesNotProjectConfig(t *testing.T) {
	service, _, _, briefs, _ := serviceMatrixFixture(StatusActive)
	briefs.config.UserID = "another-user"
	projection, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if projection.DailyBrief != nil || projection.Availability.DailyBrief.Reason != "ownership_mismatch" {
		t.Fatalf("projection = %#v", projection)
	}
}

func TestServiceGet_DoesNotRewriteTimestampsOrVersion(t *testing.T) {
	service, store, _, _, _ := serviceMatrixFixture(StatusActive)
	before := store.state.Clone()
	before.UpdatedAt = time.Now().Add(-time.Hour)
	store.state.UpdatedAt = before.UpdatedAt
	if _, err := service.Get(context.Background(), "local"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if store.state.StateVersion != before.StateVersion || !store.state.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("read changed durable version or audit timestamp")
	}
}
