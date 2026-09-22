package blueprintintake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ReintakeService refreshes changed snapshots and creates one replacement
// pending proposal. It never applies records by itself.
type ReintakeService struct {
	sources   *SourceService
	runner    *IntakeRunner
	proposals *ProposalStore
	apply     *ApplyService
	now       func() time.Time
}

func NewReintakeService(sources *SourceService, runner *IntakeRunner, proposals *ProposalStore, apply *ApplyService) *ReintakeService {
	return &ReintakeService{sources: sources, runner: runner, proposals: proposals, apply: apply, now: time.Now}
}

func (s *ReintakeService) Refresh(ctx context.Context, workspaceID, intakeKey string, mode RefreshMode, location *time.Location) (Proposal, bool, error) {
	if s == nil || s.sources == nil || s.runner == nil || s.proposals == nil {
		return Proposal{}, false, errors.New("re-intake is unavailable")
	}
	refresh, err := s.sources.RefreshSources(ctx, workspaceID, intakeKey, mode)
	if err != nil {
		return Proposal{}, false, err
	}
	if len(refresh.Changed) == 0 && len(refresh.Removed) == 0 {
		return Proposal{}, false, nil
	}
	requirement, err := s.sources.Requirement(workspaceID, intakeKey)
	if err != nil {
		return Proposal{}, false, err
	}
	results, runErr := s.runner.RunSources(ctx, workspaceID, intakeKey, refresh.Changed, nil)
	if runErr != nil {
		return Proposal{}, false, runErr
	}
	items, notice, err := proposalItemsFromResults(results, requirement, location)
	if err != nil {
		return Proposal{}, false, err
	}
	ledger, err := s.proposals.Ledger(workspaceID, requirement.Key)
	if err != nil {
		return Proposal{}, false, err
	}
	affected := make(map[string]bool, len(refresh.Changed)+len(refresh.Removed))
	for _, source := range refresh.Changed {
		affected[source.ID] = true
	}
	for _, source := range refresh.Removed {
		affected[source.ID] = true
	}
	items = classifyReintake(items, ledger, affected)
	proposal, err := finalizeProposal(workspaceID, requirement.Key, items, notice, s.now())
	if err == nil && s.apply != nil {
		err = s.apply.PrepareProposal(ctx, &proposal)
		if err == nil {
			err = rehashProposal(&proposal)
		}
	}
	if err == nil {
		err = s.proposals.Save(proposal)
	}
	return proposal, err == nil, err
}

func proposalItemsFromResults(results []SourceRunResult, requirement workspace.IntakeRequirement, location *time.Location) ([]ProposalItem, ProposalNotice, error) {
	var items []ProposalItem
	notice := ProposalNotice{}
	seen := make(map[string]bool)
	var sourceErrors []string
	for _, result := range results {
		if result.Error != "" {
			sourceErrors = append(sourceErrors, result.Error)
			continue
		}
		parsed, sourceNotice, err := ParseProposalOutput(result.Output, requirement, result.Source.ID, location, result.PartlyRead)
		if err != nil {
			sourceErrors = append(sourceErrors, err.Error())
			continue
		}
		for _, item := range parsed {
			if seen[item.Key] {
				return nil, ProposalNotice{}, ErrUnreadableProposal
			}
			seen[item.Key] = true
			items = append(items, item)
		}
		notice.Unsupported += sourceNotice.Unsupported
		notice.DroppedKind += sourceNotice.DroppedKind
		notice.Truncated = notice.Truncated || sourceNotice.Truncated
	}
	if len(items) == 0 && len(sourceErrors) > 0 {
		return nil, ProposalNotice{}, fmt.Errorf("re-intake sources failed: %s", strings.Join(sourceErrors, "; "))
	}
	if len(items) > MaxProposalItems {
		items = items[:MaxProposalItems]
		notice.Truncated = true
	}
	return items, notice, nil
}

// ReintakeAutomation coalesces watcher and daily requests per workspace and
// intake key. One run may be active and at most one follow-up is queued.
const ReintakeDomainKey = "blueprint_intake"

type ReintakeTriggerRecord struct {
	ID, WorkspaceID, Name, Path, Domain string
	Enabled                             bool
	Events                              []string
	DebounceSeconds                     int
}

type ReintakeTriggerStore interface {
	List(workspaceID string) ([]ReintakeTriggerRecord, error)
	Upsert(ReintakeTriggerRecord) (ReintakeTriggerRecord, error)
}

type ReintakeAutomation struct {
	service       *ReintakeService
	sources       *SourceService
	workspaces    WorkspaceReader
	triggers      ReintakeTriggerStore
	admissionGate *resetstate.WorkGate
	now           func() time.Time
	refreshFn     func(context.Context, string, string, RefreshMode, *time.Location) (Proposal, bool, error)

	mu        sync.Mutex
	running   map[string]bool
	followUp  map[string]RefreshMode
	lastDaily map[string]string
	stop      chan struct{}
	done      chan struct{}
	wg        sync.WaitGroup
}

func NewReintakeAutomation(service *ReintakeService, sources *SourceService, workspaces WorkspaceReader, triggers ReintakeTriggerStore) *ReintakeAutomation {
	automation := &ReintakeAutomation{service: service, sources: sources, workspaces: workspaces, triggers: triggers, now: time.Now, running: map[string]bool{}, followUp: map[string]RefreshMode{}, lastDaily: map[string]string{}}
	if service != nil {
		automation.refreshFn = service.Refresh
	}
	return automation
}

func (a *ReintakeAutomation) Approve(workspaceID, intakeKey string) error {
	if a == nil || a.sources == nil {
		return errors.New("re-intake automation is unavailable")
	}
	if err := a.sources.ApproveAutomation(workspaceID, intakeKey); err != nil {
		return err
	}
	return a.EnsureWatcher(workspaceID, intakeKey)
}

func (a *ReintakeAutomation) Status(workspaceID, intakeKey string) (bool, bool, error) {
	if a == nil || a.sources == nil {
		return false, false, nil
	}
	approved, err := a.sources.AutomationApproved(workspaceID, intakeKey)
	if err != nil || !approved {
		return approved, false, err
	}
	requirement, requirementErr := a.sources.Requirement(workspaceID, intakeKey)
	ws, workspaceErr := a.workspaces.GetFolderWorkspace(workspaceID)
	if requirementErr == nil && workspaceErr == nil && ws != nil {
		if provenance := ws.GetTemplateProvenance(); provenance != nil {
			for _, recipe := range provenance.AutomationRecipes {
				if strings.EqualFold(recipe.DirectoryKey, requirement.Sources.DirectoryKey) && recipe.Watch == nil && recipe.DailyScan != nil {
					return true, true, nil
				}
			}
		}
	}
	if a.triggers == nil {
		return approved, false, nil
	}
	records, err := a.triggers.List(workspaceID)
	if err != nil {
		return approved, false, err
	}
	for _, record := range records {
		if record.Domain == ReintakeDomainKey && record.Name == reintakeTriggerName(intakeKey) {
			return approved, record.Enabled, nil
		}
	}
	return approved, false, nil
}

func (a *ReintakeAutomation) EnsureWatcher(workspaceID, intakeKey string) error {
	if a == nil || a.triggers == nil || a.sources == nil {
		return nil
	}
	requirement, err := a.sources.Requirement(workspaceID, intakeKey)
	if err != nil {
		return err
	}
	ws, err := a.workspaces.GetFolderWorkspace(workspaceID)
	if err != nil || ws == nil {
		return errors.New("workspace is unavailable")
	}
	provenance := ws.GetTemplateProvenance()
	if provenance == nil {
		return errors.New("automation recipe is unavailable")
	}
	var recipe *workspace.AutomationRecipe
	for index := range provenance.AutomationRecipes {
		if strings.EqualFold(provenance.AutomationRecipes[index].DirectoryKey, requirement.Sources.DirectoryKey) {
			recipe = &provenance.AutomationRecipes[index]
			break
		}
	}
	if recipe == nil || recipe.Watch == nil {
		return nil
	}
	sources, err := a.sources.ListSources(workspaceID, intakeKey)
	if err != nil {
		return err
	}
	path := ""
	for _, source := range sources {
		if source.Kind == "folder" {
			path = source.FolderPath
			break
		}
	}
	if path == "" {
		return errors.New("choose a folder before approving automation")
	}
	events := append([]string(nil), recipe.Watch.Events...)
	if len(events) == 0 {
		events = []string{"create", "modify", "remove", "rename"}
	}
	debounce := recipe.Watch.DebounceSeconds
	if debounce <= 0 {
		debounce = 5
	}
	record := ReintakeTriggerRecord{WorkspaceID: workspaceID, Name: reintakeTriggerName(intakeKey), Enabled: true, Path: path, Events: events, DebounceSeconds: debounce, Domain: ReintakeDomainKey}
	existing, err := a.triggers.List(workspaceID)
	if err != nil {
		return err
	}
	for _, item := range existing {
		if item.Domain == record.Domain && item.Name == record.Name {
			record.ID = item.ID
			break
		}
	}
	_, err = a.triggers.Upsert(record)
	return err
}

func reintakeTriggerName(intakeKey string) string {
	return "Blueprint intake watch: " + strings.TrimSpace(intakeKey)
}

func (a *ReintakeAutomation) SetAdmissionGate(gate *resetstate.WorkGate) { a.admissionGate = gate }

func (a *ReintakeAutomation) HandleDomainScan(workspaceID, _ string, _ int, _ string) error {
	a.RunWorkspace(workspaceID, RefreshMode{Folders: true})
	return nil
}

func (a *ReintakeAutomation) RunWorkspace(workspaceID string, mode RefreshMode) {
	if a == nil || a.workspaces == nil {
		return
	}
	ws, err := a.workspaces.GetFolderWorkspace(workspaceID)
	if err != nil || ws == nil {
		return
	}
	provenance := ws.GetTemplateProvenance()
	if provenance == nil {
		return
	}
	for _, requirement := range provenance.IntakeRequirements {
		if requirement.Sources.DirectoryKey == "" || !hasAutomationRecipe(provenance.AutomationRecipes, requirement.Sources.DirectoryKey) {
			continue
		}
		approved, approvalErr := a.sources.AutomationApproved(workspaceID, requirement.Key)
		if approvalErr != nil || !approved {
			continue
		}
		a.RunCoalesced(workspaceID, requirement.Key, mode)
	}
}

func (a *ReintakeAutomation) RunCoalesced(workspaceID, intakeKey string, mode RefreshMode) {
	if a == nil || a.refreshFn == nil {
		return
	}
	release, err := a.admissionGate.Enter()
	if err != nil {
		return
	}
	key := runKey(workspaceID, intakeKey)
	a.mu.Lock()
	if a.running[key] {
		pending := a.followUp[key]
		pending.Folders = pending.Folders || mode.Folders
		pending.Links = pending.Links || mode.Links
		a.followUp[key] = pending
		a.mu.Unlock()
		release()
		return
	}
	a.running[key] = true
	a.wg.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.wg.Done()
		defer release()
		for {
			_, _, _ = a.refreshFn(context.Background(), workspaceID, intakeKey, mode, time.Local)
			a.mu.Lock()
			next, queued := a.followUp[key]
			delete(a.followUp, key)
			if !queued {
				delete(a.running, key)
				a.mu.Unlock()
				return
			}
			mode = next
			a.mu.Unlock()
		}
	}()
}

func (a *ReintakeAutomation) Start(workspaceIDs func() []string, interval time.Duration) {
	if a == nil || workspaceIDs == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	a.mu.Lock()
	if a.stop != nil {
		a.mu.Unlock()
		return
	}
	a.stop, a.done = make(chan struct{}), make(chan struct{})
	stop, done := a.stop, a.done
	a.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		a.tick(workspaceIDs)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				a.tick(workspaceIDs)
			}
		}
	}()
}

func (a *ReintakeAutomation) tick(workspaceIDs func() []string) {
	now := a.now()
	for _, workspaceID := range workspaceIDs() {
		ws, err := a.workspaces.GetFolderWorkspace(workspaceID)
		if err != nil || ws == nil || !a.claimDaily(ws, now) {
			continue
		}
		a.RunWorkspace(workspaceID, RefreshMode{Folders: true, Links: true})
	}
}

func (a *ReintakeAutomation) claimDaily(ws *workspace.Workspace, now time.Time) bool {
	provenance := ws.GetTemplateProvenance()
	if provenance == nil {
		return false
	}
	for _, recipe := range provenance.AutomationRecipes {
		if recipe.DailyScan == nil {
			continue
		}
		occurrence, err := workspace.LocalOccurrenceOn(recipe.DailyScan.Timezone, recipe.DailyScan.LocalTime, now)
		if err != nil || now.Before(occurrence) {
			continue
		}
		date := workspace.LocalDateKey(recipe.DailyScan.Timezone, now)
		a.mu.Lock()
		if a.lastDaily[ws.ID] == date {
			a.mu.Unlock()
			return false
		}
		a.lastDaily[ws.ID] = date
		a.mu.Unlock()
		return true
	}
	return false
}

func (a *ReintakeAutomation) Stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	stop, done := a.stop, a.done
	a.stop, a.done = nil, nil
	a.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	a.wg.Wait()
}

func hasAutomationRecipe(recipes []workspace.AutomationRecipe, directoryKey string) bool {
	for _, recipe := range recipes {
		if strings.EqualFold(strings.TrimSpace(recipe.DirectoryKey), strings.TrimSpace(directoryKey)) {
			return recipe.Watch != nil || recipe.DailyScan != nil
		}
	}
	return false
}
