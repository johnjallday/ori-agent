package workspaceupdates

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Store exposes only host bookkeeping, not ordinary user/chat/work execution.
type Store interface {
	DeliveryStore
	EnrollWelcome(context.Context, session.WelcomeScope) error
	PendingWelcomes(context.Context, int64, int) ([]session.WelcomeEnrollment, error)
	SettleWelcome(context.Context, session.WelcomeEnrollment) error
	ReadWelcome(context.Context, session.WelcomeScope) (session.WelcomeDelivery, error)
	AcknowledgeWelcome(context.Context, session.WelcomeScope, session.WelcomeAcknowledgement) error
}

type WorkspaceReader interface {
	Get(string) (*workspace.Workspace, error)
}

type ReviewReader interface {
	ReadFilingReview(context.Context, string, string) (filejanitor.FilingReview, error)
}

// Service retries only the finite, host-written delivery for explicitly
// enrolled workspaces. No GET or lifecycle callback can enroll a legacy tree.
type Service struct {
	store      Store
	workspaces WorkspaceReader
	composer   Composer
	reviews    ReviewReader
	gate       *resetstate.WorkGate
	interval   time.Duration
	wake       chan struct{}
	mu         sync.Mutex
	cancel     context.CancelFunc
	done       chan struct{}
}

func New(store Store, workspaces WorkspaceReader, composer Composer, gate *resetstate.WorkGate) *Service {
	return &Service{store: store, workspaces: workspaces, composer: composer, gate: gate, interval: 30 * time.Second, wake: make(chan struct{}, 1)}
}

// SetReviewReader wires the independent saved-state reader before Start.
func (s *Service) SetReviewReader(reader ReviewReader) { s.reviews = reader }

// ReadWorkspace combines SQLite-owned identity/version/work with folder-owned
// setup facts. Ordinary creation can write folder scaffolding without changing
// SQLite's version; those independent sequences must not deny a no-wizard
// welcome. Ready wizard snapshots, however, must have completed their mirrored
// write before recovery may use them.
func ReadWorkspace(reader WorkspaceReader, id string) (*workspace.Workspace, error) {
	primary, err := reader.Get(id)
	if err != nil || primary == nil {
		return nil, ErrNotFound
	}
	folders, ok := reader.(interface {
		GetFolderWorkspace(string) (*workspace.Workspace, error)
	})
	if !ok {
		return primary, nil
	}
	canonical, err := folders.GetFolderWorkspace(id)
	if err != nil || canonical == nil {
		return nil, ErrUnavailable
	}
	if canonical.ID != primary.ID || workspaceOwner(canonical) != workspaceOwner(primary) || canonical.Status != primary.Status || canonical.EntryAgentName() != primary.EntryAgentName() {
		return nil, ErrUnavailable
	}
	canonicalLead, canonicalUnique := canonical.FindUniqueAgentInstance(canonical.EntryAgentName(), "")
	primaryLead, primaryUnique := primary.FindUniqueAgentInstance(primary.EntryAgentName(), "")
	if canonicalUnique != primaryUnique || (canonicalUnique && canonicalLead.ID != primaryLead.ID) {
		return nil, ErrUnavailable
	}
	if canonical.SetupWizardState() == workspace.SetupWizardStateReady && canonical.Version != primary.Version {
		return nil, ErrUnavailable
	}
	// Get may return shared memory in reduced hosts. Compose a detached value
	// rather than mutating either owner during an observational read.
	encoded, err := json.Marshal(primary)
	if err != nil {
		return nil, ErrUnavailable
	}
	var out workspace.Workspace
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, ErrUnavailable
	}
	out.SetTemplateProvenance(canonical.GetTemplateProvenance())
	out.SetSetupWizardProgress(canonical.GetSetupWizardProgress())
	return &out, nil
}

func workspaceOwner(ws *workspace.Workspace) string {
	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner == "" {
		return userprofile.LocalUserID
	}
	return owner
}

// Scope resolves identities from the current canonical workspace. Names are
// presentation/routing metadata only; the unique instance ID is mandatory.
func Scope(ws *workspace.Workspace, owner string) (session.WelcomeScope, bool) {
	if ws == nil || owner == "" || workspaceOwner(ws) != owner || ws.ID == "" || ws.Kind == "group" ||
		ws.AssistantProgramState != nil || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
		return session.WelcomeScope{}, false
	}
	name := strings.TrimSpace(ws.EntryAgentName())
	lead, unique := ws.FindUniqueAgentInstance(name, "")
	if name == "" || !unique || lead.ID == "" {
		return session.WelcomeScope{}, false
	}
	return session.WelcomeScope{WorkspaceID: ws.ID, OwnerUserID: owner, AgentInstanceID: lead.ID, AgentName: name, WorkspaceVersion: ws.Version}, true
}

// Enroll runs at successful creation, before the response. A crash after this
// commit cannot lose delivery; a crash before it does not authorize finishing
// incomplete provisioning. Failed/ineligible enrollment is never guessed later.
func (s *Service) Enroll(ctx context.Context, owner, id string) error {
	ws, err := ReadWorkspace(s.workspaces, id)
	if err != nil || ws == nil {
		return ErrUnavailable
	}
	if workspaceOwner(ws) != owner {
		return ErrNotFound
	}
	scope, eligible := Scope(ws, owner)
	if !eligible {
		return nil
	}
	if err := s.store.EnrollWelcome(ctx, scope); err != nil {
		return err
	}
	s.Notify()
	return nil
}

// Notify is only a coalesced hint. It carries no private data or authority and
// is safe before Start; canonical enrolled state is always the recovery source.
func (s *Service) Notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Reconcile performs one bounded keyset page. Admission covers reads through
// final receipt/settlement writes; a reset fence refuses the entire operation.
// after=0 starts traversal; an exhausted keyset wraps without an extra interval.
func (s *Service) Reconcile(ctx context.Context, after int64) (int64, error) {
	release, err := s.gate.Enter()
	if err != nil {
		return after, err
	}
	defer release()
	items, err := s.store.PendingWelcomes(ctx, after, 32)
	if err != nil {
		return after, err
	}
	if len(items) == 0 && after > 0 {
		// Wrap in this pass rather than spending an entire interval at the
		// end of the keyset. Still at most one nonempty, bounded page of work.
		after = 0
		items, err = s.store.PendingWelcomes(ctx, after, 32)
		if err != nil {
			return after, err
		}
	}
	if len(items) == 0 {
		return 0, nil
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		after = item.ID
		ws, err := ReadWorkspace(s.workspaces, item.WorkspaceID)
		if err != nil || ws == nil {
			continue // unavailable storage is not proof of deletion
		}
		scope, eligible := Scope(ws, item.OwnerUserID)
		if !eligible || scope.AgentInstanceID != item.AgentInstanceID || scope.AgentName != item.AgentName {
			if err := s.store.SettleWelcome(ctx, item); err != nil {
				return after, err
			}
			continue
		}
		// Read saved readiness only. Calling wizard Status here would evaluate
		// adapters and could probe folders; recovery has no such capability.
		ready := ws.SetupWizardState()
		if ready != workspace.SetupWizardStateNotApplicable && ready != workspace.SetupWizardStateReady {
			continue
		}
		out, err := Deliver(ctx, s.composer, s.store, item.OwnerUserID, item.WorkspaceID)
		if err != nil || out.State != "" {
			continue // retry composition/storage failures, never original work
		}
		if !out.AwaitingAssessment {
			if err := s.store.SettleWelcome(ctx, item); err != nil {
				return after, err
			}
		}
	}
	return after, nil
}

func (s *Service) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	release, err := s.gate.EnterLifetime()
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	go func() {
		defer close(done)
		defer release()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		var after int64
		for {
			after, _ = s.Reconcile(ctx, after)
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			case <-ticker.C:
			}
		}
	}()
}

// Stop is retryable and joins the owner before the host closes any store. A
// timeout is not reported as successful teardown; reset keeps the stores open.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel == nil {
		s.mu.Unlock()
		return nil
	}
	s.cancel()
	done := s.done
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		s.mu.Lock()
		if s.done == done {
			s.cancel = nil
		}
		s.mu.Unlock()
		return nil
	}
}
