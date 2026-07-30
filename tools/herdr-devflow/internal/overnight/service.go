package overnight

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
)

// Store is the durable bridge state an Overnight Run lives in. Runs share the
// state file, and therefore the lock, with schedules and feature records: one
// writer, one atomic replace, no second store to disagree with the first.
type Store interface {
	Load() (model.BridgeState, error)
	Save(model.BridgeState) error
	Lock(context.Context) (func(), error)
}

// Service creates and inspects Overnight Runs.
type Service struct {
	Store Store
	// Now supplies the clock; nil uses time.Now.
	Now func() time.Time
	// NewID generates run identities; nil uses a random one.
	NewID func() string
}

// NewService builds a Service over the shared state directory.
func NewService(dir string) *Service { return &Service{Store: state.New(dir)} }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) newID(now time.Time) string {
	if s.NewID != nil {
		return s.NewID()
	}
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		// A collision is far less dangerous than refusing to create a run the
		// user just confirmed, and the timestamp already separates them.
		return "ovr-" + now.Format("20060102-150405")
	}
	return "ovr-" + now.Format("20060102") + "-" + hex.EncodeToString(suffix)
}

// ErrNotFound means no run with that identity exists.
var ErrNotFound = errors.New("no Overnight Run with that identity exists")

// ConfirmationInteractive and ConfirmationFlag are the two ways a run may be
// approved. Both are recorded, because a run that exists without evidence
// somebody agreed to it is exactly what must never happen.
const (
	ConfirmationInteractive = "interactive"
	ConfirmationFlag        = "confirm_flag"
)

// Create persists a confirmed plan.
//
// It takes the shared lock and re-reads state before writing, because the plan
// was built against a snapshot that is now seconds old: another terminal may
// have created a run, or delivered the continuation this plan objected to, in
// between. The conflicts are checked twice on purpose.
func (s *Service) Create(ctx context.Context, plan Plan, confirmation string) (model.OvernightRun, error) {
	if confirmation != ConfirmationInteractive && confirmation != ConfirmationFlag {
		return model.OvernightRun{}, errors.New("an Overnight Run cannot be created without an explicit confirmation")
	}
	if len(plan.Participants) == 0 {
		return model.OvernightRun{}, errors.New("an Overnight Run requires at least one selected Claude agent")
	}
	if len(plan.Conflicts) > 0 {
		return model.OvernightRun{}, fmt.Errorf("this plan cannot start: %s", plan.Conflicts[0])
	}

	release, err := s.Store.Lock(ctx)
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("lock local state: %w", err)
	}
	defer release()

	saved, err := s.Store.Load()
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("read local state: %w", err)
	}
	if conflicts := activeRunConflicts(saved, plan.RepositoryID); len(conflicts) > 0 {
		return model.OvernightRun{}, fmt.Errorf("this plan cannot start: %s", conflicts[0])
	}
	if conflicts := scheduleConflicts(plan.Participants, saved); len(conflicts) > 0 {
		return model.OvernightRun{}, fmt.Errorf("this plan cannot start: %s", conflicts[0])
	}

	now := s.now()
	run := model.OvernightRun{
		Version:      model.RunVersion,
		ID:           s.newID(now),
		RepositoryID: plan.RepositoryID,
		// A confirmed run is scheduled, never running. Nothing is prompted, no
		// wake is registered, and nothing sleeps until the supervisor picks it
		// up at its start time.
		State:        model.RunScheduled,
		CreatedAt:    now,
		StartAt:      plan.StartAt,
		DeadlineAt:   plan.DeadlineAt,
		Timezone:     plan.Timezone,
		MaxResumes:   plan.MaxResumes,
		Confirmation: confirmation,
		UpdatedAt:    now,
	}
	for index, planned := range plan.Participants {
		run.Participants = append(run.Participants, model.RunParticipant{
			ID:                fmt.Sprintf("%s-p%d", run.ID, index+1),
			Position:          index + 1,
			State:             model.ParticipantQueued,
			Feature:           planned.Feature,
			Binding:           planned.Binding,
			Checkpoint:        planned.Checkpoint,
			StartingCompleted: planned.Checkpoint.SubtasksCompleted,
			UpdatedAt:         now,
		})
	}
	run.Timeline = []model.RunEvent{{
		At:     now,
		Kind:   "created",
		Detail: fmt.Sprintf("confirmed %s with %d participant(s)", confirmation, len(run.Participants)),
	}}

	if saved.Runs == nil {
		saved.Runs = map[string]model.OvernightRun{}
	}
	saved.Runs[run.ID] = run
	if err := s.Store.Save(saved); err != nil {
		return model.OvernightRun{}, fmt.Errorf("persist the Overnight Run: %w", err)
	}
	return run, nil
}

// List returns runs for one repository, newest first. An empty repositoryID
// returns every run.
func (s *Service) List(repositoryID string) ([]model.OvernightRun, error) {
	saved, err := s.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("read local state: %w", err)
	}
	runs := make([]model.OvernightRun, 0, len(saved.Runs))
	for _, run := range saved.Runs {
		if repositoryID != "" && run.RepositoryID != repositoryID {
			continue
		}
		runs = append(runs, run)
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if !runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].CreatedAt.After(runs[j].CreatedAt)
		}
		return runs[i].ID < runs[j].ID
	})
	return runs, nil
}

// Get returns one run by identity.
func (s *Service) Get(id string) (model.OvernightRun, error) {
	saved, err := s.Store.Load()
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("read local state: %w", err)
	}
	run, ok := saved.Runs[id]
	if !ok {
		return model.OvernightRun{}, ErrNotFound
	}
	return run, nil
}

// Active returns the one non-terminal run for a repository, if there is one.
func (s *Service) Active(repositoryID string) (model.OvernightRun, bool, error) {
	runs, err := s.List(repositoryID)
	if err != nil {
		return model.OvernightRun{}, false, err
	}
	for _, run := range runs {
		if !run.State.Terminal() {
			return run, true, nil
		}
	}
	return model.OvernightRun{}, false, nil
}

// Cancel stops future unattended action for a run.
//
// It never kills an agent, never touches a worktree, and never claims more
// certainty than it has: a prompt that was in flight when cancellation arrived
// may already have been delivered, and saying otherwise would send someone to
// look at the wrong thing in the morning.
func (s *Service) Cancel(ctx context.Context, id string) (model.OvernightRun, error) {
	release, err := s.Store.Lock(ctx)
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("lock local state: %w", err)
	}
	defer release()

	saved, err := s.Store.Load()
	if err != nil {
		return model.OvernightRun{}, fmt.Errorf("read local state: %w", err)
	}
	run, ok := saved.Runs[id]
	if !ok {
		return model.OvernightRun{}, ErrNotFound
	}
	if run.State.Terminal() {
		return run, nil
	}

	now := s.now()
	var uncertainties []string
	for index := range run.Participants {
		participant := &run.Participants[index]
		switch participant.Delivery.State {
		case model.DeliveryDelivering, model.DeliveryUncertain:
			// The prompt may have arrived. Recording it as canceled would be a
			// claim nobody can support.
			participant.State = model.ParticipantUncertain
			participant.Outcome = model.ReasonUncertain
			participant.Recovery = "check this agent before prompting it again; a continuation may already have been delivered"
			uncertainties = append(uncertainties,
				participant.Feature.Name+": a continuation was in flight when the run was canceled")
		default:
			if !participant.State.Terminal() {
				participant.State = model.ParticipantCanceled
				participant.Outcome = model.ReasonCanceled
			}
		}
		participant.UpdatedAt = now
	}

	run.State = model.RunCanceled
	run.TerminalReason = model.ReasonCanceled
	run.ActiveParticipant = ""
	run.UpdatedAt = now
	if len(uncertainties) > 0 {
		run.Uncertainty = uncertainties[0]
		run.State = model.RunUncertain
		run.TerminalReason = model.ReasonUncertain
	}
	// The wake candidate is owned by the coordinator, not by this record.
	// Marking it canceled here would assert an external effect this command
	// has not performed; the supervisor withdraws it and records the outcome.
	if run.Wake.CandidateID != "" && !run.Wake.Canceled {
		run.Wake.Uncertain = true
		run.Wake.Detail = "cancellation requested; the wake candidate has not been confirmed withdrawn"
	}
	run.Timeline = append(run.Timeline, model.RunEvent{At: now, Kind: "canceled", Detail: "canceled by the user"})

	saved.Runs[id] = run
	if err := s.Store.Save(saved); err != nil {
		return model.OvernightRun{}, fmt.Errorf("persist the cancellation: %w", err)
	}
	return run, nil
}
