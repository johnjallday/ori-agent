package macwake

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/wakecoord"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	ownerName      = "Ori Agent"
	permissionTest = "Ori Agent Permission Check"
	pmsetPath      = "/usr/bin/pmset"
	osascriptPath  = "/usr/bin/osascript"
)

var macWakeLog = logger.New("macwake")

// Status describes whether Ori can program macOS wake events.
type Status struct {
	Supported             bool       `json:"supported"`
	Enabled               bool       `json:"enabled"`
	PermissionState       string     `json:"permission_state"`
	PermissionLabel       string     `json:"permission_label"`
	PermissionDetail      string     `json:"permission_detail"`
	AdminApprovalGranted  bool       `json:"admin_approval_granted"`
	DefaultLeadMinutes    int        `json:"default_lead_minutes"`
	FallbackPolicy        string     `json:"fallback_policy"`
	NextWakeAt            *time.Time `json:"next_wake_at,omitempty"`
	NextWakeTaskID        string     `json:"next_wake_task_id,omitempty"`
	LastScheduledOwner    string     `json:"last_scheduled_owner,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
	SystemScheduledEvents []string   `json:"system_scheduled_events,omitempty"`
}

// Service manages the single macOS wake event Ori owns for scheduled tasks.
type Service struct {
	configManager *config.Manager
	now           func() time.Time
	goos          func() string
	euid          func() int
	pmsetRunner   func(args []string, allowAdminPrompt bool) error
	eventLister   func() []string
	// coordinator holds wake candidates written by other Ori processes — today
	// the Herdr devflow helper's Overnight Runs. This service remains the only
	// caller of pmset; the coordinator is how anything else asks it for a wake.
	coordinator *wakecoord.Store
}

// NewService creates a macOS wake scheduling service.
func NewService(configManager *config.Manager) *Service {
	return &Service{
		configManager: configManager,
		now:           time.Now,
		goos:          func() string { return runtime.GOOS },
		euid:          os.Geteuid,
	}
}

// UseCoordinator points the service at the shared candidate store. Without one
// the service behaves exactly as it did before: workspace tasks only.
func (s *Service) UseCoordinator(store *wakecoord.Store) { s.coordinator = store }

// externalCandidates reads what other Ori processes have asked for.
//
// A failure here is deliberately quiet. The coordinator is an additional source
// of wakes, and losing it must never stop a scheduled workspace task from
// getting the wake it already had.
func (s *Service) externalCandidates() []workspace.WakeCandidate {
	if s.coordinator == nil {
		return nil
	}
	candidates, err := s.coordinator.Candidates(s.now())
	if err != nil {
		macWakeLog.Warn("Could not read shared wake candidates", logger.Fields{"error": err.Error()})
		return nil
	}
	converted := make([]workspace.WakeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		converted = append(converted, workspace.WakeCandidate{
			WorkspaceID: candidate.Source,
			TaskID:      candidate.Source + ":" + candidate.ID,
			TaskName:    candidate.Detail,
			RunAt:       candidate.WakeAt,
			// An external candidate names the instant it must be awake by; the
			// lead time it wanted is already inside that value.
			LeadMinutes: 1,
		})
	}
	return converted
}

// publishOwner states this service's own capability in the shared store.
func (s *Service) publishOwner(settings config.MacWakeSettings) {
	if s.coordinator == nil {
		return
	}
	owner := wakecoord.Owner{
		Supported:       s.goos() == "darwin",
		Enabled:         settings.Enabled,
		ApprovalGranted: s.permissionReady(settings),
	}
	if err := s.coordinator.PublishOwner(owner, s.now()); err != nil {
		macWakeLog.Warn("Could not publish wake owner state", logger.Fields{"error": err.Error()})
	}
}

// recordProgrammed tells the coordinator what was actually programmed, which is
// the only evidence another process may treat as proof that a wake exists.
func (s *Service) recordProgrammed(candidate workspace.WakeCandidate, wakeAt time.Time) {
	if s.coordinator == nil {
		return
	}
	source, id, found := strings.Cut(candidate.TaskID, ":")
	if !found {
		source, id = wakecoord.SourceWorkspaceTask, candidate.TaskID
	}
	programmed := wakecoord.Programmed{CandidateID: id, Source: source, WakeAt: wakeAt}
	if err := s.coordinator.RecordProgrammed(programmed, s.now()); err != nil {
		macWakeLog.Warn("Could not record the programmed wake", logger.Fields{"error": err.Error()})
	}
}

// Status returns the current wake scheduling capability and setting state.
func (s *Service) Status() Status {
	cfg := s.configManager.Get()
	settings := cfg.MacWake
	validateSettings(&settings)

	status := Status{
		Supported:             s.goos() == "darwin",
		Enabled:               settings.Enabled,
		AdminApprovalGranted:  settings.AdminApprovalGranted,
		DefaultLeadMinutes:    settings.DefaultLeadMinutes,
		FallbackPolicy:        settings.FallbackPolicy,
		NextWakeTaskID:        settings.LastScheduledTaskID,
		LastScheduledOwner:    settings.LastScheduledOwner,
		LastError:             settings.LastError,
		SystemScheduledEvents: s.listScheduledEvents(),
	}
	if settings.LastScheduledWakeAt != nil && !settings.LastScheduledWakeAt.IsZero() {
		nextWakeAt := *settings.LastScheduledWakeAt
		status.NextWakeAt = &nextWakeAt
	}

	switch {
	case !status.Supported:
		status.PermissionState = "unsupported"
		status.PermissionLabel = "Unsupported"
		status.PermissionDetail = "Mac wake scheduling is only available on macOS."
	case s.euid() == 0:
		status.PermissionState = "ready"
		status.PermissionLabel = "Ready"
		status.PermissionDetail = "Ori is running with permission to program macOS wake events."
	case settings.AdminApprovalGranted:
		status.PermissionState = "ready"
		status.PermissionLabel = "Ready"
		status.PermissionDetail = "Ori can request macOS admin approval when programming wake events."
	default:
		status.PermissionState = "needs_admin_approval"
		status.PermissionLabel = "Needs Admin Approval"
		status.PermissionDetail = "macOS requires administrator approval before Ori can program wake events."
	}

	return status
}

// UpdateSettings persists global Mac wake scheduling preferences.
func (s *Service) UpdateSettings(enabled *bool, leadMinutes *int, fallbackPolicy *string) (Status, error) {
	cfg := s.configManager.Get()
	next := cfg.MacWake
	if enabled != nil {
		next.Enabled = *enabled
	}
	if leadMinutes != nil {
		next.DefaultLeadMinutes = *leadMinutes
	}
	if fallbackPolicy != nil {
		next.FallbackPolicy = *fallbackPolicy
	}
	validateSettings(&next)

	cfg.MacWake = next
	if err := s.configManager.Update(cfg); err != nil {
		return s.Status(), err
	}
	if err := s.configManager.Save(); err != nil {
		return s.Status(), err
	}
	return s.Status(), nil
}

// RequestAdminApproval asks macOS for administrator approval by scheduling and
// immediately canceling a temporary wake event.
func (s *Service) RequestAdminApproval() (Status, error) {
	if s.goos() != "darwin" {
		return s.Status(), fmt.Errorf("mac wake scheduling is only available on macOS")
	}

	testAt := s.now().Add(24 * time.Hour).Truncate(time.Second)
	if err := s.scheduleWake(testAt, permissionTest); err != nil {
		s.recordLastError(err)
		return s.Status(), err
	}
	if err := s.cancelWake(testAt, permissionTest); err != nil {
		s.recordLastError(err)
		return s.Status(), err
	}

	cfg := s.configManager.Get()
	cfg.MacWake.AdminApprovalGranted = true
	cfg.MacWake.LastError = ""
	if err := s.configManager.Update(cfg); err != nil {
		return s.Status(), err
	}
	if err := s.configManager.Save(); err != nil {
		return s.Status(), err
	}
	return s.Status(), nil
}

// SyncNextWake programs one macOS wake event for the earliest wake-enabled
// scheduled task. macOS power scheduling is system-wide, so Ori owns only one
// event and recomputes it as task schedules change.
func (s *Service) SyncNextWake(candidates []workspace.WakeCandidate) error {
	if s.goos() != "darwin" {
		return nil
	}

	cfg := s.configManager.Get()
	settings := cfg.MacWake
	validateSettings(&settings)

	// Publish what this owner can do before deciding anything, so a helper
	// waiting on a wake can tell "Ori is running but cannot program wakes" from
	// "Ori is not running at all".
	s.publishOwner(settings)

	if !settings.Enabled {
		return s.cancelStoredWakeIfNeeded(cfg, settings)
	}

	// Workspace tasks and every other Ori wake source are considered together,
	// so the earliest required wake wins whichever subsystem asked for it.
	candidate, ok := s.chooseEarliestCandidate(append(append([]workspace.WakeCandidate(nil), candidates...),
		s.externalCandidates()...), settings.DefaultLeadMinutes)
	if !ok {
		if s.coordinator != nil {
			if err := s.coordinator.ClearProgrammed(s.now()); err != nil {
				macWakeLog.Warn("Could not clear the programmed wake", logger.Fields{"error": err.Error()})
			}
		}
		return s.cancelStoredWakeIfNeeded(cfg, settings)
	}
	if !s.permissionReady(settings) {
		err := fmt.Errorf("mac wake scheduling needs admin approval")
		s.recordLastError(err)
		return err
	}

	wakeAt := candidate.RunAt.Add(-time.Duration(candidate.LeadMinutes) * time.Minute)
	minWake := s.now().Add(1 * time.Minute).Truncate(time.Second)
	if wakeAt.Before(minWake) {
		wakeAt = minWake
	}
	wakeAt = wakeAt.Truncate(time.Second)

	if settings.LastScheduledWakeAt != nil &&
		settings.LastScheduledTaskID == candidate.TaskID &&
		settings.LastScheduledWakeAt.Equal(wakeAt) {
		// Already programmed. Re-record it anyway: a helper that restarted and
		// is waiting for verification needs to see the evidence, and this is
		// the only process that can honestly supply it.
		s.recordProgrammed(candidate, wakeAt)
		return nil
	}

	if err := s.cancelStoredWakeIfNeeded(cfg, settings); err != nil {
		return err
	}
	if err := s.scheduleWake(wakeAt, ownerName); err != nil {
		s.recordLastError(err)
		return err
	}

	cfg = s.configManager.Get()
	cfg.MacWake.LastScheduledWakeAt = &wakeAt
	cfg.MacWake.LastScheduledTaskID = candidate.TaskID
	cfg.MacWake.LastScheduledOwner = ownerName
	cfg.MacWake.LastError = ""
	if err := s.configManager.Update(cfg); err != nil {
		return err
	}
	if err := s.configManager.Save(); err != nil {
		return err
	}

	s.recordProgrammed(candidate, wakeAt)
	macWakeLog.Info("Scheduled macOS wake event", logger.Fields{
		"task_id": candidate.TaskID,
		"wake_at": wakeAt.Format(time.RFC3339),
	})
	return nil
}

func (s *Service) chooseEarliestCandidate(candidates []workspace.WakeCandidate, defaultLeadMinutes int) (workspace.WakeCandidate, bool) {
	filtered := make([]workspace.WakeCandidate, 0, len(candidates))
	now := s.now()
	for _, candidate := range candidates {
		if candidate.TaskID == "" || candidate.RunAt.IsZero() || !candidate.RunAt.After(now) {
			continue
		}
		if candidate.LeadMinutes <= 0 {
			candidate.LeadMinutes = defaultLeadMinutes
		}
		if candidate.LeadMinutes > 120 {
			candidate.LeadMinutes = 120
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		return workspace.WakeCandidate{}, false
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left := filtered[i].RunAt.Add(-time.Duration(filtered[i].LeadMinutes) * time.Minute)
		right := filtered[j].RunAt.Add(-time.Duration(filtered[j].LeadMinutes) * time.Minute)
		return left.Before(right)
	})
	return filtered[0], true
}

func (s *Service) cancelStoredWakeIfNeeded(cfg config.Settings, settings config.MacWakeSettings) error {
	if settings.LastScheduledWakeAt == nil || settings.LastScheduledWakeAt.IsZero() {
		return nil
	}
	if !s.permissionReady(settings) {
		err := fmt.Errorf("mac wake scheduling needs admin approval before Ori can cancel its previous wake event")
		s.recordLastError(err)
		return err
	}
	if err := s.cancelWake(*settings.LastScheduledWakeAt, ownerName); err != nil {
		s.recordLastError(err)
		return err
	}

	cfg.MacWake.LastScheduledWakeAt = nil
	cfg.MacWake.LastScheduledTaskID = ""
	cfg.MacWake.LastScheduledOwner = ""
	cfg.MacWake.LastError = ""
	if err := s.configManager.Update(cfg); err != nil {
		return err
	}
	return s.configManager.Save()
}

func (s *Service) permissionReady(settings config.MacWakeSettings) bool {
	return s.goos() == "darwin" && (s.euid() == 0 || settings.AdminApprovalGranted)
}

func (s *Service) scheduleWake(at time.Time, owner string) error {
	return s.runPMSet([]string{"schedule", "wakeorpoweron", pmsetTime(at), owner}, true)
}

func (s *Service) cancelWake(at time.Time, owner string) error {
	return s.runPMSet([]string{"schedule", "cancel", "wakeorpoweron", pmsetTime(at), owner}, true)
}

func (s *Service) runPMSet(args []string, allowAdminPrompt bool) error {
	if s.pmsetRunner != nil {
		return s.pmsetRunner(args, allowAdminPrompt)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if s.euid() == 0 || !allowAdminPrompt {
		cmd := exec.CommandContext(ctx, pmsetPath, args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("pmset %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
		return nil
	}

	shellCommand := shellQuote(pmsetPath)
	for _, arg := range args {
		shellCommand += " " + shellQuote(arg)
	}
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges`, escapeAppleScriptString(shellCommand))
	cmd := exec.CommandContext(ctx, osascriptPath, "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("admin approval for pmset failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *Service) listScheduledEvents() []string {
	if s.eventLister != nil {
		return s.eventLister()
	}
	if s.goos() != "darwin" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, pmsetPath, "-g", "sched")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	events := make([]string, 0)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Scheduled power events:") {
			continue
		}
		events = append(events, line)
	}
	return events
}

func (s *Service) recordLastError(err error) {
	if err == nil {
		return
	}
	cfg := s.configManager.Get()
	if cfg.MacWake.LastError == err.Error() {
		return
	}
	cfg.MacWake.LastError = err.Error()
	if updateErr := s.configManager.Update(cfg); updateErr != nil {
		macWakeLog.Warn("Failed to record mac wake error", logger.Fields{"error": updateErr})
		return
	}
	if saveErr := s.configManager.Save(); saveErr != nil {
		macWakeLog.Warn("Failed to save mac wake error", logger.Fields{"error": saveErr})
	}
}

func validateSettings(settings *config.MacWakeSettings) {
	if settings.DefaultLeadMinutes <= 0 {
		settings.DefaultLeadMinutes = 5
	}
	if settings.DefaultLeadMinutes > 120 {
		settings.DefaultLeadMinutes = 120
	}
	switch strings.ToLower(strings.TrimSpace(settings.FallbackPolicy)) {
	case "run_on_next_wake", "skip":
		settings.FallbackPolicy = strings.ToLower(strings.TrimSpace(settings.FallbackPolicy))
	default:
		settings.FallbackPolicy = "run_on_next_wake"
	}
}

func pmsetTime(t time.Time) string {
	return t.Format("01/02/06 15:04:05")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func escapeAppleScriptString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
