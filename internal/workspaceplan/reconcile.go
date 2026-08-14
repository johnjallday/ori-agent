package workspaceplan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Reconciliation: what a revision of already-approved work does to the Tasks
// and Runs the earlier approval created (FR-76–FR-78).
//
// The governing rule is that history is never rewritten. A revision may add
// work, and it may cancel work that has not started, but a Task that ran and
// the Run that ran it are facts. Corrective intent does not erase them; it
// creates follow-up work beside them, so the record still shows what actually
// happened and the plan still says what to do about it.

// ReconcileDisposition is what a revision does to one piece of prior work.
type ReconcileDisposition string

const (
	// DispositionRetained keeps an existing Task exactly as it is.
	DispositionRetained ReconcileDisposition = "retained"
	// DispositionCreated is new work with no prior Task.
	DispositionCreated ReconcileDisposition = "created"
	// DispositionCancel is an unstarted Task the revision drops or replaces.
	// Nothing is deleted: the Task is cancelled through the ordinary
	// transition rules and stays visible (FR-77, FR-111).
	DispositionCancel ReconcileDisposition = "cancel"
	// DispositionReplace is an unstarted Task cancelled in favor of a new Task
	// for the revised item.
	DispositionReplace ReconcileDisposition = "replace"
	// DispositionImmutable is work that started or finished. It is never
	// cancelled, mutated, or deleted (FR-78, FR-112).
	DispositionImmutable ReconcileDisposition = "immutable"
	// DispositionFollowUp is new corrective work created BESIDE immutable
	// work, because the revision changed what that work should have done and
	// the only honest remedy is to do something about it now.
	DispositionFollowUp ReconcileDisposition = "follow_up"
)

// ReconcileEntry is one line of the preview.
type ReconcileEntry struct {
	Disposition ReconcileDisposition `json:"disposition"`
	// ItemID is the Plan-local item. TaskID is the existing Task, empty for
	// created and follow-up work that does not exist yet.
	ItemID      string `json:"item_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	Description string `json:"description"`
	// Status is the existing Task's status, so the user can see why a Task is
	// immutable rather than being told it is.
	Status string `json:"status,omitempty"`
	// Reason states why this disposition applies.
	Reason string `json:"reason"`
	// Fields names what changed about a replaced item.
	Fields []string `json:"fields,omitempty"`
	// RunIDs are the Runs attached to this Task, listed so a cancellation
	// never surprises anyone about what it touches (FR-154).
	RunIDs []string `json:"run_ids,omitempty"`
}

// ReconcilePreview is the server-computed account of what confirming a
// revision would do (FR-154).
//
// It is computed on the server rather than assembled by the UI because it is
// also what the confirmation is checked against: a preview the client built
// could describe work the server would never do.
type ReconcilePreview struct {
	PlanID string `json:"plan_id"`
	// FromVersion is the approved version whose work is being reconciled, and
	// ToVersion is the version under review that would replace it.
	FromVersion int              `json:"from_version"`
	ToVersion   int              `json:"to_version"`
	Intent      RevisionIntent   `json:"intent"`
	Entries     []ReconcileEntry `json:"entries"`
	// AffectedRunIDs are every Run attached to work this reconciliation would
	// cancel or replace.
	AffectedRunIDs []string `json:"affected_run_ids,omitempty"`
	// RequiresConfirmation is true for corrective and superseding intents,
	// which can cancel work. An additive revision only adds, so it needs no
	// separate confirmation beyond the approval itself (FR-77).
	RequiresConfirmation bool `json:"requires_confirmation"`
	// Token pins the exact state this preview was computed from. A
	// confirmation carrying a token that no longer matches is refused, because
	// a Task may have started in the meantime and the preview would then
	// describe a cancellation that is no longer safe (FR-77).
	Token string `json:"token"`
	// Summary counts the entries by disposition, so a caller can render the
	// headline without walking the list.
	Summary map[ReconcileDisposition]int `json:"summary"`
}

// Reconciliation is the durable record of a user confirming one exact
// reconciliation preview (FR-77).
//
// It is separate from the approval on purpose. Approving a revised version says
// "this is the plan I want"; confirming a reconciliation says "and I accept
// that these specific started-or-not Tasks are cancelled to get there". Those
// are different decisions, and rolling them into one would let the second be
// made without ever being shown.
type Reconciliation struct {
	ID          string `json:"id"`
	PlanID      string `json:"plan_id"`
	WorkspaceID string `json:"studio_id"`
	// Token is the preview this confirmation is for. It is the primary key of
	// the decision: a preview computed from different state has a different
	// token and this confirmation does not apply to it.
	Token       string         `json:"token"`
	FromVersion int            `json:"from_version"`
	ToVersion   int            `json:"to_version"`
	Intent      RevisionIntent `json:"intent"`
	// Entries is the preview exactly as the user saw it, retained so the audit
	// record shows what was agreed to rather than what was later recomputed.
	Entries     []ReconcileEntry `json:"entries"`
	ConfirmedBy string           `json:"confirmed_by,omitempty"`
	ConfirmedAt time.Time        `json:"confirmed_at"`
	// AppliedAt marks the confirmation as spent. A confirmation authorizes one
	// reconciliation, the same way an approval authorizes one materialization.
	AppliedAt *time.Time `json:"applied_at,omitempty"`
}

// Applied reports whether this confirmation has already been spent.
func (r *Reconciliation) Applied() bool { return r != nil && r.AppliedAt != nil }

// Reconciler previews and applies revision reconciliation.
type Reconciler struct {
	service *Service
	tasks   TaskReader
	mutate  TaskMutator
}

// NewReconciler returns a reconciler over a plan service and the workspace's
// Tasks.
func NewReconciler(service *Service, tasks TaskReader, mutate TaskMutator) *Reconciler {
	return &Reconciler{service: service, tasks: tasks, mutate: mutate}
}

// Preview computes what reconciling the Plan's version under review against its
// approved work would do. It changes nothing.
func (r *Reconciler) Preview(ctx context.Context, workspaceID, planID string) (*ReconcilePreview, error) {
	plan, err := r.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.ApprovedVersion == 0 {
		return nil, fmt.Errorf(
			"%w: this plan has no approved work to reconcile against", ErrInvalidTransition)
	}
	if plan.CurrentVersion <= plan.ApprovedVersion {
		return nil, fmt.Errorf(
			"%w: request review of the revised draft before previewing reconciliation",
			ErrInvalidTransition)
	}

	store := r.service.Store()
	from, err := store.GetVersion(ctx, workspaceID, planID, plan.ApprovedVersion)
	if err != nil {
		return nil, err
	}
	to, err := store.GetVersion(ctx, workspaceID, planID, plan.CurrentVersion)
	if err != nil {
		return nil, err
	}

	ws, err := r.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}

	return buildPreview(plan, from, to, ws.Tasks), nil
}

// ConfirmInput is a user accepting one exact reconciliation preview.
type ConfirmInput struct {
	// Token is the preview the user was shown. It is required: confirming
	// "the current reconciliation" would accept whatever the state has since
	// become, which is exactly the substitution this guards against.
	Token string
	Actor string
}

// Confirm records a user's acceptance of a reconciliation preview (FR-77).
//
// It changes no Tasks. Confirming is the authorization; materializing the
// revised version is what spends it. Keeping them apart is what lets a
// materialization retry replay rather than reconcile twice.
func (r *Reconciler) Confirm(ctx context.Context, workspaceID, planID string, input ConfirmInput) (*Reconciliation, error) {
	preview, err := r.Preview(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Token) == "" {
		return nil, fmt.Errorf("%w: a confirmation must name the preview it accepts", ErrValidation)
	}
	// The token is recomputed here rather than trusted. If work moved between
	// the preview and this click, the tokens differ and the user is sent back
	// to look at what actually happens now (FR-77).
	if preview.Token != input.Token {
		return nil, fmt.Errorf(
			"%w: work changed since this preview was shown; review it again before confirming",
			ErrStalePreview)
	}
	if !preview.RequiresConfirmation {
		return nil, fmt.Errorf(
			"%w: an additive revision cancels nothing and needs no separate confirmation",
			ErrValidation)
	}

	return r.service.Store().RecordReconciliation(ctx, &Reconciliation{
		PlanID:      planID,
		WorkspaceID: workspaceID,
		Token:       preview.Token,
		FromVersion: preview.FromVersion,
		ToVersion:   preview.ToVersion,
		Intent:      preview.Intent,
		Entries:     preview.Entries,
		ConfirmedBy: input.Actor,
		ConfirmedAt: r.service.Now(),
	})
}

// Authorize checks that a revision is cleared to materialize, and returns the
// dispositions materialization must honor.
//
// It is called by the materializer rather than by a route, because the check
// has to happen at the moment the work is written. A confirmation validated a
// request earlier and then acted on later would be exactly the stale
// authorization the token exists to prevent.
func (r *Reconciler) Authorize(ctx context.Context, workspaceID, planID string) (*ReconcilePreview, *Reconciliation, error) {
	preview, err := r.Preview(ctx, workspaceID, planID)
	if err != nil {
		return nil, nil, err
	}
	if !preview.RequiresConfirmation {
		// Additive revisions add work and cancel none, so the approval of the
		// revised version is the whole authorization (FR-76).
		return preview, nil, nil
	}

	confirmation, err := r.service.Store().GetReconciliation(ctx, workspaceID, planID, preview.Token)
	if err != nil {
		if errors.Is(err, ErrReconciliationNotFound) {
			return nil, nil, fmt.Errorf(
				"%w: this revision cancels existing work and needs a confirmed reconciliation first",
				ErrReconciliationNotFound)
		}
		return nil, nil, err
	}
	if confirmation.Applied() {
		return nil, nil, ErrReconciliationConsumed
	}
	return preview, confirmation, nil
}

// Apply performs the cancellations and link retirements a confirmed preview
// describes, and spends the confirmation.
//
// It never deletes a Task, never touches one that started, and routes every
// change through the workspace's own transition rules (FR-77, FR-78, FR-111,
// FR-112).
func (r *Reconciler) Apply(ctx context.Context, workspaceID, planID string, preview *ReconcilePreview, confirmation *Reconciliation) error {
	if confirmation == nil {
		// Nothing to spend and nothing to cancel: an additive revision reaches
		// here with only created work ahead of it.
		return nil
	}
	if r.mutate == nil {
		return fmt.Errorf("%w: task mutation is not configured", ErrValidation)
	}

	now := r.service.Now()
	// Spend the confirmation FIRST. If two materializations race, the loser
	// stops here rather than cancelling the same Tasks twice.
	if err := r.service.Store().ConsumeReconciliation(ctx, workspaceID, planID, confirmation.Token, now); err != nil {
		return err
	}

	store := r.service.Store()
	for _, entry := range preview.Entries {
		if entry.Disposition != DispositionCancel && entry.Disposition != DispositionReplace {
			continue
		}
		if entry.TaskID == "" {
			continue
		}
		if err := r.cancelTask(workspaceID, entry.TaskID, entry.Reason); err != nil {
			return err
		}
		// Retiring the link records that this work belonged to a superseded
		// version. The link and the Task both stay; only their currency
		// changes (FR-78).
		reason := fmt.Sprintf("replaced by version %d", preview.ToVersion)
		if err := store.RetireTaskLink(ctx, workspaceID, planID, entry.TaskID, "", reason, now); err != nil {
			return err
		}
	}

	// Immutable work keeps its link. It is still the record of what this Plan
	// did, and retiring it would hide completed work from the Plan that caused
	// it (FR-116).
	return nil
}

// cancelTask moves one Task to cancelled through the workspace's own rules.
//
// A Task that reached a terminal state between the preview and here is left
// alone rather than failed over: the preview said it was cancellable, the world
// disagreed, and the world wins.
func (r *Reconciler) cancelTask(workspaceID, taskID, reason string) error {
	return r.mutate.MutateTask(workspaceID, taskID, func(task *workspace.Task) error {
		if isTerminalTaskStatus(task.Status) || task.Status == workspace.TaskStatusInProgress {
			return nil
		}
		task.Status = workspace.TaskStatusCancelled
		if reason != "" {
			task.Result = reason
		}
		return nil
	})
}

// buildPreview is the pure core, so the mapping rules can be tested without a
// store.
func buildPreview(plan *Plan, from, to *Version, tasks []workspace.Task) *ReconcilePreview {
	intent := to.Intent
	if intent == "" {
		intent = plan.DraftIntent
	}

	byTaskID := make(map[string]workspace.Task, len(tasks))
	for _, task := range tasks {
		byTaskID[task.ID] = task
	}
	// Live links only. A link retired by an earlier reconciliation describes
	// work a previous revision already dealt with.
	linkByItem := make(map[string]TaskLink)
	for _, link := range plan.TaskLinks {
		if link.RetiredAt != nil || link.Role == LinkRoleGroup || link.ItemID == "" {
			continue
		}
		linkByItem[link.ItemID] = link
	}
	runsByTask := make(map[string][]string)
	for _, run := range plan.RunLinks {
		if run.TaskID != "" {
			runsByTask[run.TaskID] = append(runsByTask[run.TaskID], run.RunID)
		}
	}

	changed := changedItemFieldsByID(from, to)

	preview := &ReconcilePreview{
		PlanID:      plan.ID,
		FromVersion: from.Number,
		ToVersion:   to.Number,
		Intent:      intent,
		Summary:     map[ReconcileDisposition]int{},
	}

	// Walk the revised content first, so the preview reads in the order of the
	// plan the user is looking at.
	seen := map[string]bool{}
	for _, group := range to.Content.Groups {
		for _, item := range group.Items {
			seen[item.ID] = true
			preview.add(itemEntry(item, linkByItem, byTaskID, runsByTask, changed, intent))
		}
	}

	// Then the prior work the revision no longer contains.
	for _, itemID := range sortedItemIDs(linkByItem) {
		if seen[itemID] {
			continue
		}
		preview.add(droppedEntry(itemID, linkByItem[itemID], byTaskID, runsByTask, from, intent))
	}

	preview.RequiresConfirmation = intent == RevisionCorrective || intent == RevisionSuperseding
	preview.AffectedRunIDs = affectedRuns(preview.Entries)
	preview.Token = previewToken(plan, from, to, tasks)
	return preview
}

// add appends an entry and keeps the summary in step.
func (p *ReconcilePreview) add(entry ReconcileEntry) {
	p.Entries = append(p.Entries, entry)
	p.Summary[entry.Disposition]++
}

// itemEntry decides what happens to one item that the revision still contains.
func itemEntry(
	item TaskItem,
	linkByItem map[string]TaskLink,
	byTaskID map[string]workspace.Task,
	runsByTask map[string][]string,
	changed map[string][]string,
	intent RevisionIntent,
) ReconcileEntry {
	link, linked := linkByItem[item.ID]
	if !linked {
		return ReconcileEntry{
			Disposition: DispositionCreated,
			ItemID:      item.ID,
			Description: item.Description,
			Reason:      "this step is new in the revision",
		}
	}

	task, exists := byTaskID[link.TaskID]
	if !exists {
		// The Task is gone from the workspace. Recreating it is the honest
		// repair: the plan committed to this work and nothing is running it.
		return ReconcileEntry{
			Disposition: DispositionCreated,
			ItemID:      item.ID,
			TaskID:      link.TaskID,
			Description: item.Description,
			Reason:      "the task for this step is no longer in the workspace",
		}
	}

	fields := changed[item.ID]
	entry := ReconcileEntry{
		ItemID:      item.ID,
		TaskID:      task.ID,
		Description: item.Description,
		Status:      string(task.Status),
		Fields:      fields,
		RunIDs:      runsByTask[task.ID],
	}

	// Unchanged work is retained whatever the intent. A revision that did not
	// touch a step has said nothing about it.
	if len(fields) == 0 {
		entry.Disposition = DispositionRetained
		entry.Reason = "unchanged by this revision"
		return entry
	}

	// An additive revision never disturbs prior work, even where the item text
	// moved on. Adopting that change would be a correction, and the user chose
	// not to make one (FR-76).
	if intent == RevisionAdditive {
		entry.Disposition = DispositionRetained
		entry.Reason = "changed in the revision, but an additive revision leaves existing work alone"
		return entry
	}

	if isTerminalTaskStatus(task.Status) || task.Status == workspace.TaskStatusInProgress {
		entry.Disposition = DispositionFollowUp
		entry.Reason = followUpReason(task.Status)
		return entry
	}

	entry.Disposition = DispositionReplace
	entry.Reason = "unstarted, so it is cancelled and recreated from the revised step"
	return entry
}

// droppedEntry decides what happens to prior work the revision removed.
func droppedEntry(
	itemID string,
	link TaskLink,
	byTaskID map[string]workspace.Task,
	runsByTask map[string][]string,
	from *Version,
	intent RevisionIntent,
) ReconcileEntry {
	description := itemDescription(from.Content, itemID)
	task, exists := byTaskID[link.TaskID]
	if !exists {
		return ReconcileEntry{
			Disposition: DispositionRetained,
			ItemID:      itemID,
			TaskID:      link.TaskID,
			Description: description,
			Reason:      "already gone from the workspace",
		}
	}

	entry := ReconcileEntry{
		ItemID:      itemID,
		TaskID:      task.ID,
		Description: description,
		Status:      string(task.Status),
		RunIDs:      runsByTask[task.ID],
	}

	if intent == RevisionAdditive {
		entry.Disposition = DispositionRetained
		entry.Reason = "dropped from the revision, but an additive revision cancels nothing"
		return entry
	}
	if isTerminalTaskStatus(task.Status) || task.Status == workspace.TaskStatusInProgress {
		entry.Disposition = DispositionImmutable
		entry.Reason = followUpReason(task.Status)
		return entry
	}

	entry.Disposition = DispositionCancel
	entry.Reason = "dropped from the revision and not started, so it is cancelled"
	return entry
}

// followUpReason explains why work cannot be taken back.
func followUpReason(status workspace.TaskStatus) string {
	switch status {
	case workspace.TaskStatusInProgress:
		return "already running, so it is left alone and the revision adds follow-up work instead"
	case workspace.TaskStatusCompleted:
		return "already completed, so it is left alone and the revision adds follow-up work instead"
	default:
		return "already finished, so it is left alone and the revision adds follow-up work instead"
	}
}

// changedItemFieldsByID maps item IDs to the approval-relevant fields that
// differ between two versions.
//
// It reuses the version comparison rather than diffing the content again, so
// "changed" means exactly what the review screen showed the user it meant.
func changedItemFieldsByID(from, to *Version) map[string][]string {
	changed := map[string][]string{}
	for _, change := range CompareVersions(from, to).Items {
		switch change.Kind {
		case ChangeModified, ChangeMoved:
			fields := change.Fields
			if len(fields) == 0 {
				fields = []string{"position"}
			}
			changed[change.ID] = fields
		}
	}
	return changed
}

// itemDescription finds an item's description in a version's content.
func itemDescription(content PlanContent, itemID string) string {
	if item, _, found := findItem(content, itemID); found {
		return item.Description
	}
	return itemID
}

// sortedItemIDs returns map keys in a stable order, so two previews of the same
// state are byte-identical and their tokens match.
func sortedItemIDs(links map[string]TaskLink) []string {
	ids := make([]string, 0, len(links))
	for id := range links {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// affectedRuns collects the Runs attached to work this reconciliation touches.
func affectedRuns(entries []ReconcileEntry) []string {
	seen := map[string]bool{}
	var runs []string
	for _, entry := range entries {
		switch entry.Disposition {
		case DispositionCancel, DispositionReplace, DispositionFollowUp:
			for _, runID := range entry.RunIDs {
				if !seen[runID] {
					seen[runID] = true
					runs = append(runs, runID)
				}
			}
		}
	}
	sort.Strings(runs)
	return runs
}

// previewToken pins the state a preview describes.
//
// It covers the two versions AND every linked Task's status, because the whole
// point of the token is to catch a Task that started between the preview and
// the confirmation — the one change that turns a safe cancellation into an
// unsafe one (FR-77).
func previewToken(plan *Plan, from, to *Version, tasks []workspace.Task) string {
	byTaskID := make(map[string]workspace.Task, len(tasks))
	for _, task := range tasks {
		byTaskID[task.ID] = task
	}

	var b strings.Builder
	fmt.Fprintf(&b, "plan=%s\nfrom=%d:%s\nto=%d:%s\n",
		plan.ID, from.Number, from.ContentHash, to.Number, to.ContentHash)

	links := make([]TaskLink, 0, len(plan.TaskLinks))
	links = append(links, plan.TaskLinks...)
	sort.Slice(links, func(i, j int) bool { return links[i].TaskID < links[j].TaskID })
	for _, link := range links {
		status := "missing"
		if task, exists := byTaskID[link.TaskID]; exists {
			status = string(task.Status)
		}
		retired := ""
		if link.RetiredAt != nil {
			retired = "retired"
		}
		fmt.Fprintf(&b, "task=%s:%s:%s:%s\n", link.TaskID, link.ItemID, status, retired)
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
