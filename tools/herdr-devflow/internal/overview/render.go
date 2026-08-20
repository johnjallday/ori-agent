package overview

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RenderOptions controls human output. Colour is never the only carrier of
// meaning: every phase, availability, and severity also prints as full text.
type RenderOptions struct {
	// NoColor suppresses styling entirely. It is set by --no-color and by the
	// NO_COLOR environment variable.
	NoColor bool
	// ShowAll restores Shipped, Merged (cleanup), and Unknown rows to
	// RenderCompact's table. It is set by `wt status --all`. Everything else
	// (RenderExpanded, RenderDetail, and the JSON contract) is unaffected: the
	// filter belongs to the compact table only, per Issue #348.
	ShowAll bool
}

const (
	// placeholderUnknown marks a column whose collector has not run.
	placeholderUnknown = "—"
	// maxTitleColumn bounds the feature column.
	maxTitleColumn = 34
	// maxPlanColumn bounds the plan column so one verbose row cannot stretch
	// the whole table past a normal terminal. The full text stays available in
	// the detail and expanded views.
	maxPlanColumn = 60
	// maxAgentColumn bounds the agent column. Beyond it the cell degrades to a
	// count plus the weakest binding rather than dropping an agent silently.
	maxAgentColumn = 48
)

// RenderCompact writes the feature-first overview table: one row per feature,
// grouped attention first and terminal history last.
//
// Columns that a later slice fills in are rendered as explicit placeholders
// rather than being omitted, so the table's shape does not change as
// collectors are added and a reader can see what is not yet known.
func RenderCompact(out io.Writer, snapshot Snapshot, options RenderOptions) error {
	if len(snapshot.Features) == 0 {
		return renderEmpty(out, snapshot, options)
	}
	features := activeFeatures(snapshot.Features, options)
	if len(features) == 0 {
		return renderNoActiveFeatures(out, snapshot, options)
	}
	colors := newPalette(options)

	headings := []string{"FEATURE", "PHASE", "PLAN", "GIT", "REMOTE", "AGENT", "ATTENTION"}
	rows := make([][]string, 0, len(features)+1)
	header := make([]string, len(headings))
	for index, heading := range headings {
		header[index] = colors.header(heading)
	}
	rows = append(rows, header)

	for _, feature := range features {
		// Terminal history is grouped last by the shared sort and named by the
		// PHASE column. A blank separator row is deliberately not emitted: it
		// would pad into a line of trailing whitespace.
		rows = append(rows, []string{
			colors.feature(truncate(feature.Slug, maxTitleColumn), feature.Phase.Phase.Terminal()),
			colors.phase(feature.Phase),
			colors.planCompact(feature.Plan),
			colors.git(feature.Git),
			colors.remote(feature.Remote),
			colors.agents(feature),
			colors.attention(feature),
		})
	}

	// Column widths are measured on printed width, not byte length: escape
	// sequences are invisible, and counting them is what makes colored tables
	// drift out of alignment.
	widths := make([]int, len(headings))
	for _, row := range rows {
		for index, cell := range row {
			if got := width(cell); got > widths[index] {
				widths[index] = got
			}
		}
	}

	for _, row := range rows {
		var line strings.Builder
		for index, cell := range row {
			if index == len(row)-1 {
				// Never pad the final column; that is trailing whitespace.
				line.WriteString(cell)
				break
			}
			line.WriteString(pad(cell, widths[index]+2))
		}
		if _, err := fmt.Fprintln(out, strings.TrimRight(line.String(), " ")); err != nil {
			return err
		}
	}
	// The table is feature-first by design, so agents belonging to no feature
	// get one honest summary line rather than being left out of the count.
	if err := renderUnscopedAgentsCompact(out, colors, snapshot); err != nil {
		return err
	}
	return renderFooter(out, snapshot, options)
}

// isHistoryPhase reports whether a phase is settled work that the default
// human view hides. Shipped is complete, Merged (cleanup) is only waiting on
// a `wt done` that has not run yet, and Unknown could not be placed at all —
// none of the three is something worth looking at right now.
func isHistoryPhase(phase Phase) bool {
	switch phase {
	case PhaseShipped, PhaseMergedCleanup, PhaseUnknown:
		return true
	default:
		return false
	}
}

// activeFeatures narrows to the rows worth looking at right now: history
// phases (see isHistoryPhase) are hidden unless options.ShowAll restores
// them. This is a display-only filter — it never touches the snapshot itself,
// so JSON and RenderDetail keep seeing every feature regardless.
func activeFeatures(features []Feature, options RenderOptions) []Feature {
	if options.ShowAll {
		return features
	}
	visible := make([]Feature, 0, len(features))
	for _, feature := range features {
		if isHistoryPhase(feature.Phase.Phase) {
			continue
		}
		visible = append(visible, feature)
	}
	return visible
}

// renderUnscopedAgentsCompact prints the compact table's one-line summary of
// agents that belong to no feature. It is shared between the normal table and
// renderNoActiveFeatures so those agents are never lost behind the filter.
func renderUnscopedAgentsCompact(out io.Writer, colors palette, snapshot Snapshot) error {
	unscoped := unscopedAgents(snapshot)
	if len(unscoped) == 0 {
		return nil
	}
	summary := make([]string, 0, len(unscoped))
	for _, agent := range unscoped {
		summary = append(summary, truncate(agentName(agent), 32)+" ("+agent.Scope.Label()+")")
	}
	_, err := fmt.Fprintln(out, colors.paint(
		strconv.Itoa(len(unscoped))+" agent(s) outside a feature: "+joinBounded(summary), ansiDim))
	return err
}

// renderNoActiveFeatures explains that every feature in the repository has
// settled into history, not that the repository has none: renderEmpty's
// message would be a lie here, and a bare header row with nothing under it
// would look like a bug rather than a fact.
func renderNoActiveFeatures(out io.Writer, snapshot Snapshot, options RenderOptions) error {
	colors := newPalette(options)
	if _, err := fmt.Fprintf(out,
		"No active features: all %d feature(s) are history (shipped or merged with cleanup pending). Run wt status --all to see them.\n",
		len(snapshot.Features)); err != nil {
		return err
	}
	if err := renderUnscopedAgentsCompact(out, colors, snapshot); err != nil {
		return err
	}
	return renderFooter(out, snapshot, options)
}

// RenderExpanded writes the Herdr board: every feature with its agent rows
// beneath it.
//
// It renders the same Snapshot as the compact table, so a value cannot differ
// between the two surfaces — that equality is the point of the shared
// snapshot, not an incidental property of it.
func RenderExpanded(out io.Writer, snapshot Snapshot, options RenderOptions) error {
	if len(snapshot.Features) == 0 && len(snapshot.Agents) == 0 {
		return renderEmpty(out, snapshot, options)
	}
	colors := newPalette(options)
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(out, format+"\n", args...)
		return err
	}

	managed := 0
	for _, agent := range snapshot.Agents {
		if agent.Managed {
			managed++
		}
	}
	// The agent count is the whole roster, not the feature-scoped part of it: a
	// header that counted only managed feature agents is what made agents in
	// the dev checkout look like they did not exist.
	if err := write("%s", colors.header(fmt.Sprintf(
		"Ori Devflow overview: %d feature(s), %d agent(s), %d managed",
		len(snapshot.Features), len(snapshot.Agents), managed))); err != nil {
		return err
	}

	inHistory := false
	for _, feature := range snapshot.Features {
		// Shipped work stays available but visually separate, so history never
		// competes with what is in flight.
		if feature.Phase.Phase.Terminal() && !inHistory {
			inHistory = true
			if err := write("\n%s", colors.paint("--- history ---", ansiDim)); err != nil {
				return err
			}
		}
		if err := renderExpandedFeature(write, colors, feature); err != nil {
			return err
		}
	}
	if err := renderUnscopedAgents(write, colors, snapshot); err != nil {
		return err
	}
	return renderFooter(out, snapshot, options)
}

func renderExpandedFeature(write func(string, ...any) error, colors palette, feature Feature) error {
	heading := colors.feature(feature.Slug, feature.Phase.Phase.Terminal())
	if feature.Title != "" {
		heading += colors.paint(" — "+feature.Title, ansiDim)
	}
	if err := write("\n%s", heading); err != nil {
		return err
	}
	if err := write("  phase: %s", colors.phase(feature.Phase)); err != nil {
		return err
	}
	if err := write("  plan:  %s", colors.plan(feature.Plan)); err != nil {
		return err
	}
	if next := feature.Plan.Progress.NextActionable; !next.Empty() {
		if err := write("  next:  %s %s", colors.paint(next.Ordinal, ansiBold), truncate(next.Text, 96)); err != nil {
			return err
		}
	}
	if err := write("  git:   %s", colors.git(feature.Git)); err != nil {
		return err
	}
	if feature.Git.Branch != "" {
		if err := write("  branch: %s", colors.paint(feature.Git.Branch, ansiDim)); err != nil {
			return err
		}
	}
	if err := write("  remote: %s", colors.remote(feature.Remote)); err != nil {
		return err
	}

	if len(feature.Agents) == 0 && feature.Occupancy > 0 {
		if err := write("  agents: none running (%d pane(s) open in the worktree)", feature.Occupancy); err != nil {
			return err
		}
	}
	for _, agent := range feature.Agents {
		status := "unavailable"
		if agent.StatusAvailability.OK() {
			status = string(agent.Status)
		}
		if err := write("  agent %s: status %s · binding %s",
			colors.paint(agentLabel(agent), ansiBold), status, colors.binding(agent.Binding)); err != nil {
			return err
		}
		if err := write("      %s", overnightLine(agent)); err != nil {
			return err
		}
		if agent.BindingDetail != "" && agent.Binding != BindingExact {
			if err := write("      %s", colors.paint(truncate(agent.BindingDetail, 160), ansiDim)); err != nil {
				return err
			}
		}
	}
	for _, schedule := range feature.Schedules {
		if err := write("  schedule %s: %s", schedule.ID, schedule.State); err != nil {
			return err
		}
	}
	for _, finding := range feature.Findings {
		if err := write("  [%s] %s %s", colors.severity(finding.Severity),
			colors.paint(string(finding.Code), ansiBold), finding.Message); err != nil {
			return err
		}
	}
	return nil
}

// agentLabel names an agent the way an operator would refer to it: by its
// bridge role when one claims it, and otherwise by the identity Herdr reports.
// An unmanaged agent rendered as a bare "(unmanaged)" is unactionable — there
// can be several, and none of them can be told apart.
func agentLabel(agent Agent) string {
	if agent.Role != "" {
		return agent.Role
	}
	if name := agentName(agent); name != "" {
		return truncate(name, 48) + " (unmanaged)"
	}
	return "(unmanaged)"
}

// agentName is the identity an operator recognizes, strongest first: Herdr's
// stable agent name, then its native session, then the pane it occupies.
func agentName(agent Agent) string {
	for _, candidate := range []string{
		agent.Live.Name, agent.Saved.Name, agent.Live.Session, agent.Live.Pane, agent.Saved.Pane,
	} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// overnightLine is what an agent's Overnight status reads as: its membership in
// a live run when it has one, and otherwise whether it could join one.
//
// A run in progress is the more useful fact. "Not eligible" next to an agent a
// run is actively driving would be true only in the narrow sense that it could
// not be enrolled twice, and misleading in every sense that matters.
func overnightLine(agent Agent) string {
	if agent.Run == nil {
		return eligibilityLine(agent.Eligibility)
	}
	line := "overnight: " + agent.Run.State
	if agent.Run.Active {
		line += " (queue head)"
	} else if agent.Run.QueuePosition > 0 {
		line += " (position " + strconv.Itoa(agent.Run.QueuePosition) + ")"
	}
	return line + " in run " + agent.Run.RunID
}

// eligibilityLine states, for one agent, whether an Overnight Run may control
// it and why not. It is deliberately printed for every agent: an operator
// choosing agents at bedtime needs the reason next to the agent, not in a
// separate command.
func eligibilityLine(eligibility Eligibility) string {
	line := "overnight: " + eligibility.State.Label()
	if eligibility.Reason != "" {
		line += " — " + truncate(eligibility.Reason, 140)
	}
	return line
}

// unscopedAgents are the roster rows that belong to no feature: agents working
// in a baseline or source checkout, and agents that could not be placed at all.
func unscopedAgents(snapshot Snapshot) []Agent {
	var rows []Agent
	for _, agent := range snapshot.Agents {
		if agent.Scope != AgentScopeFeature {
			rows = append(rows, agent)
		}
	}
	return rows
}

// renderUnscopedAgents prints the agents that no feature accounts for.
//
// Without this section the roster is only as complete as the feature list, and
// an agent working in the dev checkout is invisible on every surface Ori
// prints while `herdr agent list` shows it plainly.
func renderUnscopedAgents(write func(string, ...any) error, colors palette, snapshot Snapshot) error {
	rows := unscopedAgents(snapshot)
	if len(rows) == 0 {
		return nil
	}
	if err := write("\n%s", colors.header("Agents outside a feature")); err != nil {
		return err
	}
	for _, agent := range rows {
		kind := agent.Kind
		if kind == "" {
			kind = placeholderUnknown
		}
		status := "unavailable"
		if agent.StatusAvailability.OK() {
			status = string(agent.Status)
		}
		where := agent.MatchedPath
		if where == "" {
			where = "no working directory reported"
		}
		name := agentName(agent)
		if name == "" {
			name = placeholderUnknown
		}
		if err := write("  %s (%s): status %s · %s · %s",
			colors.paint(truncate(name, 48), ansiBold), kind, status, agent.Scope.Label(), truncatePath(where, 96)); err != nil {
			return err
		}
		if err := write("      %s", overnightLine(agent)); err != nil {
			return err
		}
	}
	return nil
}

func renderEmpty(out io.Writer, snapshot Snapshot, options RenderOptions) error {
	if _, err := fmt.Fprintln(out, "No features were found in this repository."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Looked for planning artifacts and feature worktrees."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Ideas that have not been planned yet live in GitHub Issues: ./scripts/devops.sh"); err != nil {
		return err
	}
	// A repository with no features can still have agents open in it, and those
	// are exactly the agents nothing else would show.
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(out, format+"\n", args...)
		return err
	}
	if err := renderUnscopedAgents(write, newPalette(options), snapshot); err != nil {
		return err
	}
	return renderFooter(out, snapshot, options)
}

// renderFooter states the snapshot's completeness and every repository-scoped
// finding. An incomplete snapshot must say so; silence would read as health.
func renderFooter(out io.Writer, snapshot Snapshot, options RenderOptions) error {
	colors := newPalette(options)
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	status := "complete"
	if !snapshot.Complete {
		status = "INCOMPLETE"
	}
	if snapshot.Stale {
		status += " (stale)"
	}
	line := "Snapshot: " + colors.snapshotStatus(status, snapshot.Complete) +
		colors.paint(" · generated "+snapshot.GeneratedAt.Format("2006-01-02 15:04:05"), ansiDim)
	if _, err := fmt.Fprintln(out, line); err != nil {
		return err
	}

	for _, source := range snapshot.Sources {
		if source.Availability.OK() {
			continue
		}
		detail := source.Detail
		if detail == "" {
			detail = source.Availability.Label()
		}
		marker := colors.paint("  - ", ansiDim)
		if source.Required {
			marker = colors.paint("  ! ", ansiRed, ansiBold)
		}
		if _, err := fmt.Fprintln(out, marker+colors.paint(string(source.Kind), ansiBold)+": "+detail); err != nil {
			return err
		}
	}
	for _, finding := range snapshot.Findings {
		if _, err := fmt.Fprintln(out, "  ["+colors.severity(finding.Severity)+"] "+finding.Message); err != nil {
			return err
		}
	}
	return nil
}

func phaseCell(state PhaseState) string {
	label := state.Phase.Label()
	if !state.Confirmed {
		// An unconfirmed phase is a local guess. Saying so is the difference
		// between a diagnostic and a lie.
		label += " (unconfirmed)"
	}
	return label
}

func planCell(plan Plan) string {
	switch {
	case plan.Copy == PlanCopyNone:
		return "no plan"
	case plan.PRDAvailability == AvailabilityAbsent && plan.TaskListAvailability != AvailabilityAbsent:
		return "no PRD"
	case plan.TaskListAvailability == AvailabilityAbsent && plan.PRDAvailability != AvailabilityAbsent:
		return "no task list"
	case plan.PRDAvailability == AvailabilityMalformed || plan.TaskListAvailability == AvailabilityMalformed:
		return "malformed"
	case plan.Progress.Availability.OK():
		return progressCell(plan.Progress)
	default:
		// Hierarchical progress arrives with the plan-parsing slice.
		return placeholderUnknown
	}
}

func progressCell(progress PlanProgress) string {
	cell := strconv.Itoa(progress.MilestonesCompleted) + "/" + strconv.Itoa(progress.MilestonesTotal) + " milestones"
	if progress.SubtasksTotal > 0 {
		cell += " · " + strconv.Itoa(progress.SubtasksCompleted) + "/" + strconv.Itoa(progress.SubtasksTotal) + " subtasks"
	}
	switch {
	case progress.ImplementationComplete && progress.DeliveryCheckpointsRemaining > 0:
		// Saying "next 7.4" here would imply implementation work remains when
		// only delivery steps do. Delivery-only wording never names a Group.
		cell += " · delivery only (" + strconv.Itoa(progress.DeliveryCheckpointsRemaining) + " left)"
	case progress.NextActionable.Ordinal != "":
		cell += " · "
		if group := groupLabel(progress.ActiveMilestone.Ordinal); group != "" {
			cell += group + " "
		}
		cell += "next " + progress.NextActionable.Ordinal
	}
	return cell
}

// groupLabel names the active parent milestone the way an operator refers to
// it in conversation: "G8", never a title. It takes only the parent ordinal
// ("8.0" -> "G8") and returns "" for an empty or noncanonical ordinal, so a
// malformed or missing ActiveMilestone degrades to the plain "next" wording
// instead of printing a bogus group.
func groupLabel(ordinal string) string {
	parent, _, found := strings.Cut(ordinal, ".")
	if !found || parent == "" {
		return ""
	}
	if _, err := strconv.Atoi(parent); err != nil {
		return ""
	}
	return "G" + parent
}

// RenderDetail writes the expanded view for one feature: provenance, planning
// paths, the full active milestone and next action, delivery checkpoints, Git
// evidence, and every finding. It is what `wt status --feature <slug>` prints.
func RenderDetail(out io.Writer, snapshot Snapshot, feature Feature, options RenderOptions) error {
	colors := newPalette(options)
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(out, format+"\n", args...)
		return err
	}

	title := colors.feature(feature.Slug, feature.Phase.Phase.Terminal())
	if feature.Title != "" {
		title += colors.paint(" — "+feature.Title, ansiDim)
	}
	if err := write("%s", title); err != nil {
		return err
	}
	if err := write("Phase: %s", colors.phase(feature.Phase)); err != nil {
		return err
	}
	if feature.Phase.Reason != "" {
		if err := write("  Reason: %s", feature.Phase.Reason); err != nil {
			return err
		}
	}
	if len(feature.Sources) > 0 {
		kinds := make([]string, 0, len(feature.Sources))
		for _, kind := range feature.Sources {
			kinds = append(kinds, string(kind))
		}
		if err := write("  Evidence: %s", strings.Join(kinds, ", ")); err != nil {
			return err
		}
	}

	if err := renderDetailPlan(write, colors, feature.Plan); err != nil {
		return err
	}
	if err := renderDetailGit(write, colors, feature.Git); err != nil {
		return err
	}

	if err := write("\nRemote: %s", colors.remote(feature.Remote)); err != nil {
		return err
	}
	if err := renderDetailAgents(write, colors, feature); err != nil {
		return err
	}

	if len(feature.Findings) == 0 {
		if err := write("\nFindings: none"); err != nil {
			return err
		}
	} else if err := write("\nFindings:"); err != nil {
		return err
	}
	for _, finding := range feature.Findings {
		if err := write("  [%s] %s %s", colors.severity(finding.Severity),
			colors.paint(string(finding.Code), ansiBold), finding.Message); err != nil {
			return err
		}
		if finding.Detail != "" {
			if err := write("      %s", colors.paint(finding.Detail, ansiDim)); err != nil {
				return err
			}
		}
	}
	return renderFooter(out, snapshot, options)
}

func renderDetailPlan(write func(string, ...any) error, colors palette, plan Plan) error {
	if err := write("\nPlan: %s", colors.plan(plan)); err != nil {
		return err
	}
	if plan.Copy != PlanCopyNone {
		if err := write("  Authoritative copy: %s", plan.Copy); err != nil {
			return err
		}
	}
	if plan.PRDPath != "" {
		if err := write("  PRD: %s", plan.PRDPath); err != nil {
			return err
		}
	}
	if plan.TaskListPath != "" {
		if err := write("  Task list: %s", plan.TaskListPath); err != nil {
			return err
		}
	}
	progress := plan.Progress
	if progress.ParseIssue != "" {
		if err := write("  Parse issue: %s", progress.ParseIssue); err != nil {
			return err
		}
	}
	if !progress.Availability.OK() {
		return nil
	}
	if !progress.ActiveMilestone.Empty() {
		if err := write("  Active milestone: %s %s", colors.paint(progress.ActiveMilestone.Ordinal, ansiBold), progress.ActiveMilestone.Text); err != nil {
			return err
		}
	}
	switch {
	case !progress.NextActionable.Empty():
		if err := write("  Next: %s %s", colors.paint(progress.NextActionable.Ordinal, ansiBold), progress.NextActionable.Text); err != nil {
			return err
		}
	case progress.ImplementationComplete:
		if err := write("  Next: implementation is complete; only delivery checkpoints remain"); err != nil {
			return err
		}
	}
	if len(progress.DeliveryCheckpoints) > 0 {
		if err := write("  Delivery checkpoints in %s (%d remaining across the whole plan):",
			progress.ActiveMilestone.Ordinal, progress.DeliveryCheckpointsRemaining); err != nil {
			return err
		}
		for _, checkpoint := range progress.DeliveryCheckpoints {
			if err := write("    %s %s", checkpoint.Ordinal, checkpoint.Text); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderDetailGit(write func(string, ...any) error, colors palette, git GitState) error {
	if err := write("\nGit: %s", colors.git(git)); err != nil {
		return err
	}
	if git.WorktreePath != "" {
		if err := write("  Worktree: %s", git.WorktreePath); err != nil {
			return err
		}
	}
	if git.Branch != "" {
		if err := write("  Branch: %s", git.Branch); err != nil {
			return err
		}
	}
	if git.HeadSHA != "" {
		if err := write("  HEAD: %s", truncate(git.HeadSHA, 12)); err != nil {
			return err
		}
	}
	if git.BaselineStale {
		if err := write("  Note: the local baseline is behind its remote, so these counts understate divergence"); err != nil {
			return err
		}
	}
	if git.Detail != "" {
		if err := write("  Degraded: %s", git.Detail); err != nil {
			return err
		}
	}
	return nil
}

func gitCell(git GitState) string {
	switch git.Availability {
	case AvailabilityAbsent:
		return "no worktree"
	case AvailabilityUnavailable:
		return "unavailable"
	case AvailabilityUnknown:
		return placeholderUnknown
	}
	parts := []string{}
	if git.DivergenceAvailability.OK() {
		parts = append(parts, "+"+strconv.Itoa(git.Ahead)+"/-"+strconv.Itoa(git.Behind))
	} else {
		parts = append(parts, "divergence unavailable")
	}
	switch {
	case !git.DirtyAvailability.OK():
		parts = append(parts, "state unavailable")
	case git.Dirty:
		parts = append(parts, "dirty")
	default:
		parts = append(parts, "clean")
	}
	return strings.Join(parts, " ")
}

func remoteCell(remote Remote) string {
	switch remote.Availability {
	case AvailabilityUnavailable:
		return "unavailable"
	case AvailabilityAbsent:
		return "no PR"
	case AvailabilityStale:
		return "stale"
	case AvailabilityUnknown:
		return placeholderUnknown
	}
	if remote.PullRequest == nil {
		return "no PR"
	}
	cell := "#" + strconv.Itoa(remote.PullRequest.Number)
	if remote.PullRequest.Draft {
		cell += " draft"
	}
	return cell + " " + remote.PullRequest.Checks.Label()
}

// agentCell summarizes a feature's agents. Binding health is shown alongside
// status because an "idle" agent whose identity no longer resolves is a very
// different thing from an idle agent that does.
func agentCell(feature Feature) string {
	if len(feature.Agents) == 0 {
		if feature.Occupancy > 0 {
			// A pane is sitting in the worktree with no agent running. That is
			// occupancy, not an agent, and it matters for cleanup.
			return "no agent (" + strconv.Itoa(feature.Occupancy) + " pane)"
		}
		return "none"
	}

	cells := make([]string, 0, len(feature.Agents))
	for _, agent := range feature.Agents {
		cells = append(cells, agentSummary(agent))
	}
	full := strings.Join(cells, ", ")
	if width(full) <= maxAgentColumn {
		return full
	}
	// Too many to spell out. Report the count and the weakest binding present,
	// so a drifted agent among healthy ones is never hidden by truncation.
	return strconv.Itoa(len(feature.Agents)) + " agents (" + weakestBinding(feature).Label() + ")"
}

// agentSummary renders one agent as "<role> <status>[ (<binding>)]".
func agentSummary(agent Agent) string {
	label := agent.Role
	if !agent.Managed {
		label = "unmanaged"
	}
	if label == "" {
		label = "agent"
	}
	status := "unavailable"
	if agent.StatusAvailability.OK() {
		status = string(agent.Status)
	}
	cell := label + " " + status
	// "missing (missing)" says the same thing twice; every other binding state
	// adds information the status alone does not carry.
	redundant := agent.Binding == BindingMissing && agent.Status == AgentMissing
	if agent.Binding != BindingExact && agent.Binding != "" && !redundant {
		cell += " (" + agent.Binding.Label() + ")"
	}
	return cell
}

// weakestBinding returns the least healthy binding among a feature's agents.
// One drifted role among healthy ones is the thing worth surfacing.
func weakestBinding(feature Feature) BindingHealth {
	worst := BindingExact
	rank := map[BindingHealth]int{
		BindingExact: 0, BindingUnavailable: 1, BindingPossibleDrift: 2,
		BindingMissing: 3, BindingAmbiguous: 4,
	}
	for _, agent := range feature.Agents {
		if rank[agent.Binding] > rank[worst] {
			worst = agent.Binding
		}
	}
	return worst
}

// renderDetailAgents prints each role with its saved record and live
// observation kept visibly separate, so a bridge record can never be mistaken
// for something that is actually running.
func renderDetailAgents(write func(string, ...any) error, colors palette, feature Feature) error {
	if len(feature.Agents) == 0 {
		if feature.Occupancy > 0 {
			return write("\nAgents: none running, %d pane(s) open in the worktree", feature.Occupancy)
		}
		return write("\nAgents: none")
	}
	if err := write("\nAgents:"); err != nil {
		return err
	}
	for _, agent := range feature.Agents {
		status := "unavailable"
		if agent.StatusAvailability.OK() {
			status = string(agent.Status)
		}
		if err := write("  %s — status %s · binding %s",
			colors.paint(agentLabel(agent), ansiBold), status, colors.binding(agent.Binding)); err != nil {
			return err
		}
		if agent.Kind != "" {
			if err := write("      kind: %s", agent.Kind); err != nil {
				return err
			}
		}
		if !agent.Saved.Empty() {
			if err := write("      bridge record: %s", describeIdentity(agent.Saved)); err != nil {
				return err
			}
		}
		if !agent.Live.Empty() {
			if err := write("      observed live: %s", describeIdentity(agent.Live)); err != nil {
				return err
			}
		}
		if agent.MatchedPath != "" {
			if err := write("      matched worktree: %s", agent.MatchedPath); err != nil {
				return err
			}
		}
		if err := write("      %s", eligibilityLine(agent.Eligibility)); err != nil {
			return err
		}
		if agent.BindingDetail != "" {
			if err := write("      %s", agent.BindingDetail); err != nil {
				return err
			}
		}
		for _, candidate := range agent.BindingCandidates {
			if err := write("      candidate: %s", describeIdentity(candidate)); err != nil {
				return err
			}
		}
		if !agent.LastActivityAt.IsZero() {
			if err := write("      last activity: %s", agent.LastActivityAt.Format("2006-01-02 15:04:05")); err != nil {
				return err
			}
		}
	}
	for _, schedule := range feature.Schedules {
		if err := write("  schedule %s — %s", schedule.ID, schedule.State); err != nil {
			return err
		}
	}
	return nil
}

func describeIdentity(identity Identity) string {
	var parts []string
	appendPart := func(label, value string) {
		if value != "" {
			parts = append(parts, label+" "+value)
		}
	}
	appendPart("workspace", identity.Workspace)
	appendPart("pane", identity.Pane)
	appendPart("terminal", identity.Terminal)
	appendPart("session", identity.Session)
	appendPart("kind", identity.Kind)
	if len(parts) == 0 {
		return "(no identity fields)"
	}
	return strings.Join(parts, ", ")
}

// attentionCell shows the highest-severity finding for a feature, with a count
// when more than one exists, so nothing is hidden behind a single line.
func attentionCell(feature Feature) string {
	severity, has := feature.Attention()
	if !has {
		return "ok"
	}
	cell := severity.Label() + ": " + string(feature.Findings[0].Code)
	if extra := len(feature.Findings) - 1; extra > 0 {
		cell += " (+" + strconv.Itoa(extra) + ")"
	}
	return cell
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

// truncatePath keeps the end of a path rather than the start. Every checkout in
// a repository shares its leading directories, so trimming from the left is
// what leaves an unreadable prefix and drops the part that identifies it.
func truncatePath(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return "…" + string(runes[len(runes)-limit+1:])
}
