package blueprintintake

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type RunStatus struct {
	ID          string              `json:"id"`
	WorkspaceID string              `json:"workspace_id"`
	IntakeKey   string              `json:"intake_key"`
	State       string              `json:"state"`
	Sources     []SourceRunProgress `json:"sources,omitempty"`
	Error       string              `json:"error,omitempty"`
	Proposal    *Proposal           `json:"proposal,omitempty"`
	StartedAt   time.Time           `json:"started_at"`
	FinishedAt  *time.Time          `json:"finished_at,omitempty"`
}

type activeRun struct {
	status RunStatus
	cancel context.CancelFunc
}

// Service coordinates source runs, proposal storage, cancellation and apply.
type Service struct {
	sources   *SourceService
	runner    *IntakeRunner
	proposals *ProposalStore
	apply     *ApplyService
	now       func() time.Time

	mu   sync.Mutex
	runs map[string]*activeRun
}

func NewService(sources *SourceService, runner *IntakeRunner, proposals *ProposalStore, apply *ApplyService) *Service {
	return &Service{sources: sources, runner: runner, proposals: proposals, apply: apply, now: time.Now, runs: make(map[string]*activeRun)}
}

func (s *Service) BundledSkillReview(workspaceID, intakeKey string) (BundledSkillReview, bool, error) {
	if s == nil || s.runner == nil {
		return BundledSkillReview{}, false, errors.New("intake service is unavailable")
	}
	return s.runner.BundledSkillReview(workspaceID, intakeKey)
}

func (s *Service) TrustBundledSkill(workspaceID, intakeKey, choice string) (BundledSkillReview, error) {
	if s == nil || s.runner == nil {
		return BundledSkillReview{}, errors.New("intake service is unavailable")
	}
	return s.runner.TrustBundledSkill(workspaceID, intakeKey, choice)
}

func (s *Service) SkillReadiness(workspaceID, intakeKey string) (SkillReadiness, error) {
	if s == nil || s.runner == nil {
		return SkillReadiness{}, errors.New("intake service is unavailable")
	}
	return s.runner.SkillReadiness(workspaceID, intakeKey)
}

func (s *Service) Start(workspaceID, intakeKey string, location *time.Location) (RunStatus, error) {
	if s == nil || s.runner == nil || s.proposals == nil {
		return RunStatus{}, errors.New("intake service is unavailable")
	}
	requirement, err := s.sources.Requirement(workspaceID, intakeKey)
	if err != nil {
		return RunStatus{}, err
	}
	consent, err := s.sources.ConsentStatus(context.Background(), workspaceID, requirement.Key)
	if err != nil {
		return RunStatus{}, err
	}
	if !consent.Accepted {
		return RunStatus{}, errors.New("accept the content-reading statement before running the intake skill")
	}
	key := runKey(workspaceID, requirement.Key)
	s.mu.Lock()
	if current := s.runs[key]; current != nil && current.status.State == "running" {
		status := cloneRunStatus(current.status)
		s.mu.Unlock()
		return status, errors.New("this intake is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := s.now().UTC()
	run := &activeRun{status: RunStatus{ID: fmt.Sprintf("%d", started.UnixNano()), WorkspaceID: workspaceID, IntakeKey: requirement.Key, State: "running", StartedAt: started}, cancel: cancel}
	s.runs[key] = run
	status := cloneRunStatus(run.status)
	s.mu.Unlock()
	go s.executeRequirement(ctx, key, run, location)
	return status, nil
}

func (s *Service) executeRequirement(ctx context.Context, key string, run *activeRun, location *time.Location) {
	requirement, err := s.sources.Requirement(run.status.WorkspaceID, run.status.IntakeKey)
	if err != nil {
		s.finishRun(key, run, nil, err)
		return
	}
	results, runErr := s.runner.Run(ctx, run.status.WorkspaceID, run.status.IntakeKey, func(update SourceRunProgress) {
		s.recordProgress(key, run, update)
	})
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		s.finishRun(key, run, nil, runErr)
		return
	}
	var items []ProposalItem
	notice := ProposalNotice{}
	seen := make(map[string]bool)
	for _, result := range results {
		if result.Error != "" {
			s.finishRun(key, run, nil, errors.New(result.Error))
			return
		}
		parsed, sourceNotice, parseErr := ParseProposalOutput(result.Output, requirement, result.Source.ID, location, result.PartlyRead)
		if parseErr != nil {
			s.finishRun(key, run, nil, parseErr)
			return
		}
		for _, item := range parsed {
			if seen[item.Key] {
				s.finishRun(key, run, nil, ErrUnreadableProposal)
				return
			}
			seen[item.Key] = true
			items = append(items, item)
		}
		notice.Unsupported += sourceNotice.Unsupported
		notice.DroppedKind += sourceNotice.DroppedKind
		notice.Truncated = notice.Truncated || sourceNotice.Truncated
	}
	if len(items) > MaxProposalItems {
		items = items[:MaxProposalItems]
		notice.Truncated = true
	}
	proposal, err := finalizeProposal(run.status.WorkspaceID, requirement.Key, items, notice, s.now())
	if err == nil && s.apply != nil {
		err = s.apply.PrepareProposal(ctx, &proposal)
		if err == nil {
			err = rehashProposal(&proposal)
		}
	}
	if err == nil {
		err = s.proposals.Save(proposal)
	}
	if err == nil && runErr != nil {
		err = runErr
	}
	s.finishRun(key, run, &proposal, err)
}

func (s *Service) recordProgress(key string, run *activeRun, update SourceRunProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runs[key] != run {
		return
	}
	for index := range run.status.Sources {
		if run.status.Sources[index].SourceID == update.SourceID {
			run.status.Sources[index] = update
			return
		}
	}
	run.status.Sources = append(run.status.Sources, update)
}

func (s *Service) finishRun(key string, run *activeRun, proposal *Proposal, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runs[key] != run {
		return
	}
	finished := s.now().UTC()
	run.status.FinishedAt = &finished
	if proposal != nil && proposal.Hash != "" {
		run.status.Proposal = proposal
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			run.status.State = "cancelled"
		} else {
			run.status.State = "failed"
		}
		run.status.Error = err.Error()
		return
	}
	run.status.State = "done"
}

func (s *Service) Status(workspaceID, intakeKey string) (RunStatus, error) {
	if s == nil {
		return RunStatus{}, errors.New("intake service is unavailable")
	}
	key := runKey(workspaceID, intakeKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.runs[key]; run != nil {
		return cloneRunStatus(run.status), nil
	}
	return RunStatus{}, errors.New("no intake run has started")
}

func (s *Service) Cancel(workspaceID, intakeKey string) (RunStatus, error) {
	key := runKey(workspaceID, intakeKey)
	s.mu.Lock()
	run := s.runs[key]
	if run == nil || run.status.State != "running" {
		s.mu.Unlock()
		return RunStatus{}, errors.New("no intake run is active")
	}
	run.cancel()
	status := cloneRunStatus(run.status)
	s.mu.Unlock()
	return status, nil
}

func (s *Service) Proposal(workspaceID, intakeKey string) (Proposal, error) {
	return s.proposals.Get(workspaceID, intakeKey)
}

func (s *Service) Apply(workspaceID, intakeKey string, request ApplyRequest) (Proposal, error) {
	return s.apply.Apply(workspaceID, intakeKey, request)
}

func (s *Service) ApplyContext(ctx context.Context, workspaceID, intakeKey string, request ApplyRequest) (Proposal, error) {
	return s.apply.ApplyContext(ctx, workspaceID, intakeKey, request)
}

func (s *Service) Skip(workspaceID, intakeKey, hash string) (Proposal, error) {
	return s.proposals.Skip(workspaceID, intakeKey, hash)
}

func runKey(workspaceID, intakeKey string) string { return workspaceID + "\x00" + intakeKey }

func cloneRunStatus(status RunStatus) RunStatus {
	status.Sources = append([]SourceRunProgress(nil), status.Sources...)
	if status.Proposal != nil {
		proposal := *status.Proposal
		proposal.Items = append([]ProposalItem(nil), status.Proposal.Items...)
		status.Proposal = &proposal
	}
	return status
}
