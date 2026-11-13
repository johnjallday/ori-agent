package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The app assistant's read turn (assistant-reads-projects FR30 to FR39).
//
// Every trigger is a scan. A completed scan hands the songs that still need
// reading, and that the Home's app plugin can read, to one read job per Home.
// The job runs one bounded model turn for the Home's recorded shared assistant,
// offering exactly one tool, read_listed_songs, built for that turn only and
// offered to no other agent. The tool takes no argument: it reads the scan's own
// targets in batches through the plugin, saves facts through the scan-keyed
// fenced write, and returns counts only. The scan's library.scan_completed
// event is held until the read ends, so the Manager's brief sees the facts.
// There is no fallback reader: every way the read cannot run is a stated reason.

// Receipt statuses and reasons.
const (
	factsReadStarted  = "started"
	factsReadFinished = "finished"
	factsReadStopped  = "stopped"

	FactsReadNoModel          = "no_model"
	FactsReadAssistantMissing = "assistant_missing"
	FactsReadNoAssistant      = "no_assistant"
	FactsReadPluginUpdate     = "plugin_update"
	FactsReadPluginFailed     = "plugin_failed"
	FactsReadTimeLimit        = "time_limit"
	FactsReadRestarted        = "restarted"
	FactsReadDetailsOff       = "details_off"
	FactsReadNoCall           = "no_call"
	FactsReadModelError       = "model_error"

	// Triggers.
	FactsReadByLearn = "learn"
	FactsReadByScan  = "scan"

	maxFactsReads = 8
	// factsReadTokenBudget gates only the one nudge: a turn is about 650
	// tokens per call, and its prompt and tool result do not grow with the
	// number of songs.
	factsReadTokenBudget = 8000
	factsReadMaxTokens   = 256
	factsReadCallTimeout = 60 * time.Second
	// factsReadSaveEvery saves facts every 64 songs, so a stop keeps what was read.
	factsReadSaveEvery = 64
	// FactsReadToolName is the turn's one tool.
	FactsReadToolName = "read_listed_songs"
)

// factsReadPassBudget bounds one read's wall time; a call has its own 60 s.
var factsReadPassBudget = 10 * time.Minute

var factsReadReasons = map[string]bool{
	"": true, FactsReadNoModel: true, FactsReadAssistantMissing: true, FactsReadNoAssistant: true,
	FactsReadPluginUpdate: true, FactsReadPluginFailed: true, FactsReadTimeLimit: true, FactsReadRestarted: true,
	FactsReadDetailsOff: true, FactsReadNoCall: true, FactsReadModelError: true,
}

// FactsReadRun is one read turn's durable receipt. Counts only, never a path.
type FactsReadRun struct {
	ID         string     `json:"id"`
	Boot       string     `json:"boot"`
	Trigger    string     `json:"trigger"`
	ScanIDs    []string   `json:"scan_ids,omitempty"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	AgentName  string     `json:"agent_name,omitempty"`
	Model      string     `json:"model,omitempty"`
	Targets    int        `json:"targets"`
	Read       int        `json:"read"`
	TooLarge   int        `json:"too_large"`
	Unreadable int        `json:"unreadable"`
	Changed    int        `json:"changed"`
	Left       int        `json:"left"`
	Tokens     int        `json:"tokens,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func factsReadsValid(runs []FactsReadRun) bool {
	if len(runs) > maxFactsReads {
		return false
	}
	seen := map[string]bool{}
	for _, run := range runs {
		if run.ID == "" || !validText(run.ID, 80) || seen[run.ID] || !validText(run.Boot, 80) || run.Boot == "" ||
			(run.Trigger != FactsReadByLearn && run.Trigger != FactsReadByScan) || len(run.ScanIDs) > 64 ||
			!validText(run.AgentName, 160) || !validText(run.Model, 160) || !factsReadReasons[run.Reason] ||
			run.Targets < 0 || run.Read < 0 || run.TooLarge < 0 || run.Unreadable < 0 || run.Changed < 0 || run.Left < 0 ||
			run.Tokens < 0 || run.StartedAt.IsZero() || (run.FinishedAt != nil && run.FinishedAt.Before(run.StartedAt)) {
			return false
		}
		for _, id := range run.ScanIDs {
			if id == "" || !validText(id, 160) {
				return false
			}
		}
		switch run.Status {
		case factsReadStarted:
			if run.FinishedAt != nil || run.Reason != "" {
				return false
			}
		case factsReadFinished, factsReadStopped:
			if run.FinishedAt == nil {
				return false
			}
		default:
			return false
		}
		seen[run.ID] = true
	}
	return true
}

func pruneFactsReads(doc *Document) {
	for len(doc.FactsReads) >= maxFactsReads {
		dropped := false
		for i, run := range doc.FactsReads {
			if run.Status != factsReadStarted {
				doc.FactsReads = append(doc.FactsReads[:i], doc.FactsReads[i+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			return
		}
	}
}

// presentFactsRead shows a receipt a previous process left started as stopped
// by a restart, without writing (the startup sweep makes it durable).
func presentFactsRead(run FactsReadRun, boot string) FactsReadRun {
	if run.Status == factsReadStarted && run.Boot != boot {
		run.Status, run.Reason = factsReadStopped, FactsReadRestarted
	}
	return run
}

// currentFactsReadBoot identifies this process, so a receipt from another one
// that is still "started" is known to have been cut off.
var currentFactsReadBoot = newID()

// LatestFactsRead is the Home's newest read receipt, as shown now; false when
// there is none.
func (s *Store) LatestFactsRead(scope Scope) (FactsReadRun, bool) {
	doc, err := s.Read(scope)
	if err != nil {
		return FactsReadRun{}, false
	}
	latest, ok := latestFactsRead(doc)
	if !ok {
		return FactsReadRun{}, false
	}
	return presentFactsRead(latest, currentFactsReadBoot), true
}

// latestFactsRead is the document's newest read receipt.
func latestFactsRead(doc Document) (FactsReadRun, bool) {
	if len(doc.FactsReads) == 0 {
		return FactsReadRun{}, false
	}
	latest := doc.FactsReads[0]
	for _, run := range doc.FactsReads[1:] {
		if run.StartedAt.After(latest.StartedAt) {
			latest = run
		}
	}
	return latest, true
}

func (s *Store) writeFactsRead(scope Scope, key, action string, mutate func(current *Document) (string, error)) error {
	digest := proposalRunOpDigest(scope, key)
	for attempt := 0; ; attempt++ {
		doc, err := s.Read(scope)
		if err != nil {
			return err
		}
		_, _, err = s.mutateWithHomePolicy(scope, doc.Revision, operation{key: key, action: action, digest: digest}, nil, mutate)
		if errors.Is(err, ErrConflict) && attempt < 2 {
			continue
		}
		if errors.Is(err, errNoRunChange) {
			return nil
		}
		return err
	}
}

// claimFactsRead records a receipt: started (the turn runs) or already stopped
// (a preflight that could not run it, with its reason).
func (s *Store) claimFactsRead(scope Scope, run FactsReadRun) error {
	return s.writeFactsRead(scope, "facts-read:"+run.ID, "facts_read_claim", func(current *Document) (string, error) {
		for _, existing := range current.FactsReads {
			if existing.ID == run.ID {
				return "", errNoRunChange
			}
		}
		pruneFactsReads(current)
		current.FactsReads = append(current.FactsReads, run)
		return run.ID, nil
	})
}

// finishFactsRead closes a started receipt with its outcome.
func (s *Store) finishFactsRead(scope Scope, final FactsReadRun) error {
	return s.writeFactsRead(scope, "facts-read-finish:"+final.ID, "facts_read_finish", func(current *Document) (string, error) {
		for i := range current.FactsReads {
			run := &current.FactsReads[i]
			if run.ID != final.ID {
				continue
			}
			if run.Status != factsReadStarted {
				return "", errNoRunChange
			}
			at := s.now().UTC()
			if at.Before(run.StartedAt) {
				at = run.StartedAt
			}
			kept := *run
			*run = final
			run.StartedAt, run.Boot, run.Trigger, run.ScanIDs = kept.StartedAt, kept.Boot, kept.Trigger, kept.ScanIDs
			run.FinishedAt = &at
			return final.ID, nil
		}
		return "", ErrConflict
	})
}

// sweepFactsReads durably stops every receipt another process left started.
func (s *Store) sweepFactsReads(scope Scope, boot string) (int, error) {
	doc, err := s.Read(scope)
	if err != nil {
		return 0, err
	}
	var stale []string
	for _, run := range doc.FactsReads {
		if run.Status == factsReadStarted && run.Boot != boot {
			stale = append(stale, run.ID)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	closed := 0
	err = s.writeFactsRead(scope, "facts-read-sweep:"+strings.Join(stale, ",")+":"+boot, "facts_read_sweep", func(current *Document) (string, error) {
		closed = 0
		at := s.now().UTC()
		for i := range current.FactsReads {
			run := &current.FactsReads[i]
			if run.Status != factsReadStarted || run.Boot == boot {
				continue
			}
			when := at
			if when.Before(run.StartedAt) {
				when = run.StartedAt
			}
			run.Status, run.Reason, run.FinishedAt = factsReadStopped, FactsReadRestarted, &when
			closed++
		}
		if closed == 0 {
			return "", errNoRunChange
		}
		return "sweep", nil
	})
	return closed, err
}

// FactsReadTeam is the Home's song team as the read turn needs it.
type FactsReadTeam struct {
	PluginID, BlueprintID, Label string
}

// FactsReadHost is what the read turn needs from the server.
type FactsReadHost struct {
	// Team is the Home's song team from the installed app plugin.
	Team func(scope Scope) (FactsReadTeam, bool)
	// Assistant is the Home's recorded shared assistant, or the reason there
	// is none (FactsReadNoAssistant, FactsReadAssistantMissing).
	Assistant func(homeID string, team FactsReadTeam) (name, reason string)
	// Model binds the assistant's own model.
	Model func(ctx context.Context, homeID, agentName string) (ManagerChat, string, error)
	// Reader calls a plugin's project reader.
	Reader ProjectFactsCaller
}

// FactsReadSlice is one completed scan's part of a read: the songs the Home's
// app plugin can read that still need reading, and the scan's held event.
type FactsReadSlice struct {
	scope   Scope
	scan    Scan
	root    Root
	targets []songFactsTarget
	grant   songFactsGrant
	publish func()
}

// FactsReadCollector gathers the slices of several scans (a "Learn them"
// listing), so one press makes one turn.
type FactsReadCollector struct {
	mu     sync.Mutex
	slices []FactsReadSlice
}

type factsReadCollectorKey struct{}

// WithDeferredFactsRead makes the scans committed under ctx hand their reads
// to the returned collector instead of starting them.
func WithDeferredFactsRead(ctx context.Context) (context.Context, *FactsReadCollector) {
	collector := &FactsReadCollector{}
	return context.WithValue(ctx, factsReadCollectorKey{}, collector), collector
}

func factsReadCollectorFrom(ctx context.Context) *FactsReadCollector {
	collector, _ := ctx.Value(factsReadCollectorKey{}).(*FactsReadCollector)
	return collector
}

func (c *FactsReadCollector) add(slice FactsReadSlice) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slices = append(c.slices, slice)
}

// Slices are the collected slices. A song listed by two scans keeps only the
// newest scan's target.
func (c *FactsReadCollector) Slices() []FactsReadSlice {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]bool{}
	out := make([]FactsReadSlice, len(c.slices))
	for i := len(c.slices) - 1; i >= 0; i-- {
		slice := c.slices[i]
		var kept []songFactsTarget
		for _, target := range slice.targets {
			key := slice.root.ID + "\x00" + target.Relative
			if !seen[key] {
				seen[key] = true
				kept = append(kept, target)
			}
		}
		slice.targets = kept
		out[i] = slice
	}
	return out
}

// Targets is how many songs the collected slices would read.
func (c *FactsReadCollector) Targets() int {
	total := 0
	for _, slice := range c.Slices() {
		total += len(slice.targets)
	}
	return total
}

// FactsReadRunner runs the read turns, one at a time per Home in this process.
type FactsReadRunner struct {
	roots *Roots
	host  FactsReadHost
	boot  string

	mu      sync.Mutex
	homes   map[string]*sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewFactsReadRunner binds the runner to the Home's roots and the server.
func NewFactsReadRunner(roots *Roots, host FactsReadHost) *FactsReadRunner {
	return &FactsReadRunner{roots: roots, host: host, boot: currentFactsReadBoot,
		homes: map[string]*sync.Mutex{}, cancels: map[string]context.CancelFunc{}}
}

// SetFactsReadRunner makes completed scans hand their reads to runner.
func (r *Roots) SetFactsReadRunner(runner *FactsReadRunner) { r.factsRead = runner }

func (r *FactsReadRunner) homeLock(homeID string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock := r.homes[homeID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.homes[homeID] = lock
	}
	return lock
}

// Stop cancels the read running for a Home (its song details were switched
// off). Nothing from the batch in flight is kept.
func (r *FactsReadRunner) Stop(homeID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel := r.cancels[homeID]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Enqueue starts a scan's read in the background. Its event is published when
// the read ends, whatever happened.
func (r *FactsReadRunner) Enqueue(slice FactsReadSlice) {
	go r.Read(context.Background(), []FactsReadSlice{slice}, FactsReadByScan)
}

// Read runs one read turn over the slices (all of one Home) and returns its
// receipt. The slices' held events are published once it ends.
func (r *FactsReadRunner) Read(ctx context.Context, slices []FactsReadSlice, trigger string) FactsReadRun {
	defer func() {
		for _, slice := range slices {
			if slice.publish != nil {
				slice.publish()
			}
		}
	}()
	if r == nil || r.roots == nil || len(slices) == 0 {
		return FactsReadRun{}
	}
	scope := slices[0].scope
	lock := r.homeLock(scope.HomeID)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	r.cancels[scope.HomeID] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.cancels, scope.HomeID)
		r.mu.Unlock()
	}()

	run := FactsReadRun{ID: newID(), Boot: r.boot, Trigger: trigger, Status: factsReadStarted,
		StartedAt: r.roots.library.now().UTC()}
	for _, slice := range slices {
		run.ScanIDs = append(run.ScanIDs, slice.scan.ID)
		run.Targets += len(slice.targets)
	}
	stopped := func(reason string) FactsReadRun {
		at := run.StartedAt
		run.Status, run.Reason, run.FinishedAt, run.Left = factsReadStopped, reason, &at, run.Targets
		if err := r.roots.library.claimFactsRead(scope, run); err != nil {
			logger.Warn("Could not record a song read that did not start", logger.Fields{"home_id": scope.HomeID, "error": err.Error()})
		}
		return run
	}
	// Preflight: consent, reader, assistant, model. No fallback in any case.
	if !r.grantActive(scope) {
		return stopped(FactsReadDetailsOff)
	}
	readers := r.readers()
	if len(readers) == 0 {
		return stopped(FactsReadPluginUpdate)
	}
	team, ok := FactsReadTeam{}, false
	if r.host.Team != nil {
		team, ok = r.host.Team(scope)
	}
	if !ok || r.host.Assistant == nil {
		return stopped(FactsReadNoAssistant)
	}
	name, reason := r.host.Assistant(scope.HomeID, team)
	if reason != "" {
		return stopped(reason)
	}
	run.AgentName = name
	if r.host.Model == nil {
		return stopped(FactsReadNoModel)
	}
	chat, model, err := r.host.Model(ctx, scope.HomeID, name)
	if err != nil || chat == nil {
		return stopped(FactsReadNoModel)
	}
	run.Model = model
	if err := r.roots.library.claimFactsRead(scope, run); err != nil {
		logger.Warn("Could not start a song read", logger.Fields{"home_id": scope.HomeID, "error": err.Error()})
		return run
	}
	job := &factsReadJob{runner: r, scope: scope, slices: slices, readers: readers, run: &run, team: team}
	run.Reason, run.Tokens = r.turn(ctx, chat, model, team.Label, job)
	run.Status = factsReadFinished
	if run.Reason != "" {
		run.Status = factsReadStopped
	}
	if !job.called {
		run.Left = run.Targets
	}
	if err := r.roots.library.finishFactsRead(scope, run); err != nil {
		logger.Warn("Could not finish a song read receipt", logger.Fields{"home_id": scope.HomeID, "error": err.Error()})
	}
	return run
}

func (r *FactsReadRunner) grantActive(scope Scope) bool {
	if r.roots == nil || r.roots.library == nil || r.roots.library.workspaces == nil {
		return false
	}
	home, err := r.roots.library.workspaces.Get(scope.HomeID)
	if err != nil || home == nil {
		return false
	}
	_, ok := songFactsGrantOf(home.GetAssistantProgramState())
	return ok
}

func (r *FactsReadRunner) readers() map[string]projectReader {
	if r.roots.library.installed == nil {
		return nil
	}
	installed, err := r.roots.library.installed.List()
	if err != nil {
		return nil
	}
	return projectReadersFrom(installed)
}

// factsReadPrompt is the same at any number of songs: no counts, no names.
func factsReadPrompt(label string) (system, user string) {
	if strings.TrimSpace(label) == "" {
		label = "the project assistant"
	}
	return "You are " + label + ", this Home's project assistant. The owner approved reading song details. " +
			"Call " + FactsReadToolName + " once: Ori reads every listed song that still needs it and gives you counts. Then stop.",
		"Songs in this Home's listing need reading."
}

func factsReadToolSpec() llm.Tool {
	return llm.Tool{Name: FactsReadToolName,
		Description: "Read every song in this Home's listing that still needs reading. Takes no arguments; returns counts only.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}
}

// turn is at most two model calls: one that should call the tool, and one
// nudge when it did not. The tool's result ends the turn.
func (r *FactsReadRunner) turn(ctx context.Context, chat ManagerChat, model, label string, job *factsReadJob) (reason string, tokens int) {
	system, user := factsReadPrompt(label)
	messages := []llm.Message{{Role: llm.RoleUser, Content: user}}
	for step := 0; step < 2; step++ {
		callCtx, cancel := context.WithTimeout(ctx, factsReadCallTimeout)
		response, err := chat(callCtx, llm.ChatRequest{Model: model, SystemPrompt: system, Messages: messages,
			Tools: []llm.Tool{factsReadToolSpec()}, Temperature: 0, MaxTokens: factsReadMaxTokens})
		cancel()
		if err != nil || response == nil {
			if ctx.Err() != nil {
				return FactsReadDetailsOff, tokens
			}
			return FactsReadModelError, tokens
		}
		used := response.Usage.TotalTokens
		if parts := response.Usage.PromptTokens + response.Usage.CompletionTokens; parts > used {
			used = parts
		}
		tokens += max(used, 0)
		if len(response.ToolCalls) == 0 {
			if step == 0 && tokens <= factsReadTokenBudget {
				messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: response.Content},
					llm.Message{Role: llm.RoleUser, Content: "Call " + FactsReadToolName + " now."})
				continue
			}
			return FactsReadNoCall, tokens
		}
		for _, call := range response.ToolCalls {
			if call.Name != FactsReadToolName || job.called || !emptyArguments(call.Arguments) {
				continue // anything else is answered by no one: the turn ends with the read
			}
			job.read(ctx)
			return job.reason, tokens
		}
		return FactsReadNoCall, tokens
	}
	return FactsReadNoCall, tokens
}

func emptyArguments(arguments string) bool {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return true
	}
	var value map[string]any
	return json.Unmarshal([]byte(trimmed), &value) == nil && len(value) == 0
}

// factsReadJob is the tool's work: the scans' targets, read in batches.
type factsReadJob struct {
	runner  *FactsReadRunner
	scope   Scope
	slices  []FactsReadSlice
	readers map[string]projectReader
	run     *FactsReadRun
	team    FactsReadTeam
	called  bool
	reason  string
}

// FactsReadCounts is what read_listed_songs gives the agent: counts only.
type FactsReadCounts struct {
	Read         int    `json:"read"`
	TooLarge     int    `json:"too_large"`
	Unreadable   int    `json:"unreadable"`
	Changed      int    `json:"changed"`
	LeftForLater int    `json:"left_for_later"`
	Stopped      string `json:"stopped,omitempty"`
}

func (j *factsReadJob) read(parent context.Context) FactsReadCounts {
	j.called = true
	r := j.runner
	ctx, cancel := context.WithTimeout(parent, factsReadPassBudget)
	defer cancel()
	report := songFactsProgressFrom(parent)
	done := 0
	progress := func(song string) {
		if report != nil {
			report(SongFactsProgress{Song: song, Done: done, Targets: j.run.Targets, Read: j.run.Read,
				Skipped: done - j.run.Read, Reader: j.run.AgentName})
		}
	}
	stop := func(reason string) { j.reason = reason }
	// The tool's own start re-checks the consent and the assistant.
	if !r.grantActive(j.scope) {
		stop(FactsReadDetailsOff)
	} else if r.host.Assistant != nil {
		if _, reason := r.host.Assistant(j.scope.HomeID, j.team); reason != "" {
			stop(reason)
		}
	}
	for _, slice := range j.slices {
		if j.reason != "" {
			break
		}
		var pending []songFactsRead
		chunk := 0
		save := func() bool {
			if len(pending) == 0 {
				return true
			}
			chunk++
			suffix := ":" + j.run.ID + ":" + strconv.Itoa(chunk)
			if _, _, err := r.roots.saveSongFactsWith(slice.scope, slice.scan, pending, slice.grant, suffix); err != nil {
				stop(FactsReadDetailsOff)
				return false
			}
			pending = nil
			return true
		}
		targets := slice.targets
		for len(targets) > 0 && j.reason == "" {
			reader, ok := j.readers[targets[0].Format]
			if !ok {
				targets = targets[1:]
				continue
			}
			// One call carries one reader's format only.
			format := targets[0].Format
			batch := []songFactsTarget{}
			for len(targets) > 0 && len(batch) < reader.batch && targets[0].Format == format {
				batch = append(batch, targets[0])
				targets = targets[1:]
			}
			progress(songFactsName(batch[0]))
			reads, counts, err := r.readBatch(ctx, slice, batch, reader)
			switch {
			case errors.Is(err, errFactsReadAuthority):
				stop(FactsReadDetailsOff)
			case err != nil && parent.Err() != nil:
				stop(FactsReadDetailsOff)
			case err != nil && ctx.Err() != nil:
				stop(FactsReadTimeLimit)
			case err != nil:
				stop(FactsReadPluginFailed)
			}
			if err != nil {
				break
			}
			pending = append(pending, reads...)
			j.run.Read += counts.Read
			j.run.TooLarge += counts.TooLarge
			j.run.Unreadable += counts.Unreadable
			j.run.Changed += counts.Changed
			j.run.Left += counts.Failed
			done += len(batch)
			progress("")
			if len(pending) >= factsReadSaveEvery && !save() {
				break
			}
		}
		if j.reason == "" {
			save()
		}
	}
	j.run.Left = max(j.run.Targets-j.run.Read-j.run.TooLarge-j.run.Unreadable-j.run.Changed, 0)
	if report != nil {
		report(SongFactsProgress{Done: done, Targets: j.run.Targets, Read: j.run.Read, Skipped: done - j.run.Read,
			Finished: true, Stopped: j.reason, Reader: j.run.AgentName})
	}
	return FactsReadCounts{Read: j.run.Read, TooLarge: j.run.TooLarge, Unreadable: j.run.Unreadable,
		Changed: j.run.Changed, LeftForLater: j.run.Left, Stopped: j.reason}
}

var errFactsReadAuthority = errors.New("song details or the root changed")

// readBatch reads one batch under the root gate, with the root and the
// consent checked before and after; a batch read while either changed is
// dropped.
func (r *FactsReadRunner) readBatch(ctx context.Context, slice FactsReadSlice, batch []songFactsTarget,
	reader projectReader) ([]songFactsRead, projectFactsBatchCounts, error) {
	gate := rootAccessGate(slice.scope)
	gate.RLock()
	defer gate.RUnlock()
	if !r.roots.songFactsAuthorityNoGate(slice.scope, slice.root, slice.grant) {
		return nil, projectFactsBatchCounts{}, errFactsReadAuthority
	}
	reads, counts, err := readProjectFactsBatch(ctx, slice.root, batch, slice.grant, reader, r.host.Reader)
	if err != nil {
		return nil, projectFactsBatchCounts{}, err
	}
	if !r.roots.songFactsAuthorityNoGate(slice.scope, slice.root, slice.grant) {
		return nil, projectFactsBatchCounts{}, errFactsReadAuthority
	}
	return reads, counts, nil
}

// Sweep durably stops every read receipt a previous process left started, on
// every Home with a library. It is run once at startup and never starts a turn.
func (r *FactsReadRunner) Sweep() int {
	if r == nil || r.roots == nil || r.roots.library == nil || r.roots.library.workspaces == nil {
		return 0
	}
	ids, err := r.roots.library.workspaces.List()
	if err != nil {
		return 0
	}
	closed := 0
	for _, id := range ids {
		home, getErr := r.roots.library.workspaces.Get(id)
		if getErr != nil || home == nil {
			continue
		}
		state := home.GetAssistantProgramState()
		if state == nil || len(state.ProjectLibrary) == 0 {
			continue
		}
		key := state.Key.Normalize()
		scope := Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
		if !scope.valid() {
			continue
		}
		n, sweepErr := r.roots.library.sweepFactsReads(scope, r.boot)
		if sweepErr != nil {
			continue
		}
		closed += n
	}
	return closed
}

// afterScanFacts runs after a completed scan is recorded. Songs whose format an
// installed app plugin reads go to the read turn (one reader per song, FR39),
// and the scan's event waits for it. The host has no reader of its own, so a
// song no installed plugin reads is not read at all; the page says why through
// ReaderStatus. With nothing for a plugin to read, the event goes now.
func (r *Roots) afterScanFacts(ctx context.Context, scope Scope, scan Scan, observed Discovery, root Root, digest LibraryDigest) {
	publish := func() { r.library.publishScanCompleted(scope, digest) }
	targets, grant, ok := r.songFactsTargets(scope, scan, observed)
	if !ok || r.factsRead == nil {
		publish()
		return
	}
	readers := r.factsRead.readers()
	var covered []songFactsTarget
	for _, target := range targets {
		if _, ok := readers[target.Format]; ok {
			covered = append(covered, target)
		}
	}
	if len(covered) == 0 {
		publish()
		return
	}
	sort.SliceStable(covered, func(i, j int) bool { return covered[i].Format < covered[j].Format })
	slice := FactsReadSlice{scope: scope, scan: scan, root: root, targets: covered, grant: grant, publish: publish}
	if collector := factsReadCollectorFrom(ctx); collector != nil {
		collector.add(slice)
		return
	}
	r.factsRead.Enqueue(slice)
}

// FactsReadAllowed reports whether a Home's consent is active, for a caller
// that wants to say why a read did not start.
func FactsReadAllowed(state *workspace.AssistantProgramState) bool {
	_, ok := songFactsGrantOf(state)
	return ok
}
