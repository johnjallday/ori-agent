package overview

import (
	"sort"
	"strconv"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/planning"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/tasklist"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// Input is everything the union inventory joins. Every field is optional: a
// collector that failed contributes nothing rather than blanking the result,
// and the caller records that failure as an unavailable Source.
type Input struct {
	// DevPlanning is the dev worktree's tasks directory scan. It is planning
	// input before `wt start` and archived history after `wt done`.
	DevPlanning planning.Set
	// Checkouts is the linked-worktree inventory.
	Checkouts worktree.Inventory
	// BridgeSlugs are features with a saved bridge record.
	BridgeSlugs []string
	// HerdrSlugs are features Herdr reports a workspace for.
	HerdrSlugs []string
	// GitHubSlugs are features with candidate remote branches or PRs.
	GitHubSlugs []string
	// LookupActivePlan reads the authoritative planning copy from inside a
	// feature's own worktree. It is injected so the join stays testable and so
	// no arbitrary path is ever read. Nil disables active-copy selection.
	LookupActivePlan func(worktreePath, slug string) (planning.Feature, error)
	// ReadPlanProgress parses one task list into hierarchical progress. It is
	// injected for the same reasons. Nil leaves progress unknown rather than
	// reporting a plan as having no work done.
	ReadPlanProgress func(taskListPath string) tasklist.Plan
	// Now is the observation time stamped onto derived evidence.
	Now time.Time
}

// BuildInventory joins every source on exact normalized slugs and returns one
// skeleton feature row per slug, plus the findings raised by the join itself.
//
// The join never guesses. When two checkouts claim one slug, or when a
// checkout's branch and directory disagree, every piece of evidence is
// retained and an ambiguity finding is emitted instead of a winner.
func BuildInventory(input Input) ([]Feature, []Finding) {
	slugs := map[string]map[SourceKind]struct{}{}
	add := func(slug string, kind SourceKind) {
		if slug == "" || !planning.ValidSlug(slug) {
			return
		}
		if _, ok := slugs[slug]; !ok {
			slugs[slug] = map[SourceKind]struct{}{}
		}
		slugs[slug][kind] = struct{}{}
	}

	for slug := range input.DevPlanning.Features {
		add(slug, SourcePlanning)
	}
	// A row exists because something real exists: a plan, a checkout, a bridge
	// record, a live agent, or a branch on GitHub. An unselected idea is not a
	// feature — it is an Issue, and `wt backlog` is where those are read.
	for slug := range input.Checkouts.Features {
		add(slug, SourceWorktree)
	}
	for _, slug := range input.BridgeSlugs {
		add(slug, SourceBridge)
	}
	for _, slug := range input.HerdrSlugs {
		add(slug, SourceHerdr)
	}
	for _, slug := range input.GitHubSlugs {
		add(slug, SourceGitHub)
	}

	ordered := make([]string, 0, len(slugs))
	for slug := range slugs {
		ordered = append(ordered, slug)
	}
	sort.Strings(ordered)

	features := make([]Feature, 0, len(ordered))
	var findings []Finding
	for _, slug := range ordered {
		feature, raised := buildFeature(slug, sourceList(slugs[slug]), input)
		features = append(features, feature)
		findings = append(findings, raised...)
	}
	findings = append(findings, pathCollisions(features)...)
	return features, findings
}

// pathCollisions reports two features resolving to one canonical worktree.
// Agents are bound by path, so a collision would attribute one agent's work to
// both features; reporting beats guessing which is meant.
func pathCollisions(features []Feature) []Finding {
	byPath := map[string][]string{}
	for _, feature := range features {
		if feature.Git.WorktreePath == "" {
			continue
		}
		byPath[feature.Git.WorktreePath] = append(byPath[feature.Git.WorktreePath], feature.Slug)
	}
	var findings []Finding
	paths := make([]string, 0, len(byPath))
	for path, slugs := range byPath {
		if len(slugs) > 1 {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		slugs := byPath[path]
		sort.Strings(slugs)
		for _, slug := range slugs {
			findings = append(findings, Finding{
				Code:     FindingWorktreePathCollision,
				Severity: SeverityError,
				Feature:  slug,
				Source:   SourceWorktree,
				Message:  "More than one feature resolves to the same worktree path; agents cannot be attributed.",
				Detail:   "Path " + path + " is claimed by " + joinBounded(slugs) + ".",
			})
		}
	}
	return findings
}

func sourceList(set map[SourceKind]struct{}) []SourceKind {
	order := []SourceKind{SourcePlanning, SourceWorktree, SourceGit, SourceGitHub, SourceBridge, SourceHerdr}
	kinds := make([]SourceKind, 0, len(set))
	for _, kind := range order {
		if _, ok := set[kind]; ok {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func buildFeature(slug string, sources []SourceKind, input Input) (Feature, []Finding) {
	feature := Feature{Slug: slug, Sources: sources}
	var findings []Finding

	checkouts := input.Checkouts.Features[slug]
	active, ambiguous := selectCheckout(checkouts)
	if ambiguous {
		paths := make([]string, 0, len(checkouts))
		for _, checkout := range checkouts {
			paths = append(paths, checkout.Path)
		}
		sort.Strings(paths)
		findings = append(findings, Finding{
			Code:     FindingIdentityAmbiguous,
			Severity: SeverityError,
			Feature:  slug,
			Source:   SourceWorktree,
			Message:  strconv.Itoa(len(checkouts)) + " checkouts claim this feature; none was chosen.",
			Detail:   "Claiming checkouts: " + joinBounded(paths),
		})
	}
	if active != nil {
		feature.Git = GitState{
			Availability: AvailabilityUnknown,
			WorktreePath: planning.Sanitize(active.Path, maxIdentityPathRunes),
			Branch:       planning.Sanitize(active.Branch, 200),
			HeadSHA:      planning.Sanitize(active.Head, 64),
			ObservedAt:   input.Checkouts.ObservedAt,
		}
		// A directory renamed away from its branch is real drift: `wt done`
		// and the Herdr board both key off the directory name.
		if active.SlugOrigin == worktree.SlugOriginBranch && active.PathSlug != "" && active.PathSlug != slug {
			findings = append(findings, Finding{
				Code:     FindingNameMismatch,
				Severity: SeverityWarning,
				Feature:  slug,
				Source:   SourceWorktree,
				Message:  "The worktree directory name does not match its feature branch.",
				Detail:   "Branch slug " + slug + "; directory slug " + active.PathSlug + ".",
			})
		}
	}

	feature.Plan = selectPlan(slug, active, input)
	if feature.Plan.Title != "" {
		feature.Title = feature.Plan.Title
	}
	return feature, findings
}

// selectCheckout returns the single feature checkout for a slug. Two or more
// claims are ambiguous and resolve to no checkout at all, because picking one
// would silently attribute Git state to the wrong directory.
func selectCheckout(checkouts []worktree.Checkout) (*worktree.Checkout, bool) {
	switch len(checkouts) {
	case 0:
		return nil, false
	case 1:
		return &checkouts[0], false
	default:
		return nil, true
	}
}

// selectPlan picks the authoritative planning copy. While a feature worktree
// exists its copy is authoritative, because that is the copy being ticked off
// during implementation. Once the worktree is gone, `wt done` has archived the
// ticked copy back into dev, so the dev copy becomes authoritative again.
func selectPlan(slug string, active *worktree.Checkout, input Input) Plan {
	if active != nil && input.LookupActivePlan != nil {
		found, err := input.LookupActivePlan(active.Path, slug)
		if err == nil && (found.PRD.Exists() || found.TaskList.Exists()) {
			return planFrom(found, PlanCopyActive, input.Now, input.ReadPlanProgress)
		}
	}
	if devFeature, ok := input.DevPlanning.Feature(slug); ok {
		return planFrom(devFeature, PlanCopyDev, input.DevPlanning.ObservedAt, input.ReadPlanProgress)
	}
	return Plan{
		Copy:                 PlanCopyNone,
		PRDAvailability:      planAvailability(planning.StateAbsent),
		TaskListAvailability: planAvailability(planning.StateAbsent),
		Progress:             PlanProgress{Availability: AvailabilityAbsent},
		ObservedAt:           input.Now,
	}
}

func planFrom(found planning.Feature, source PlanCopy, observedAt time.Time, read func(string) tasklist.Plan) Plan {
	plan := Plan{
		Copy:                 source,
		PRDPath:              found.PRD.Path,
		TaskListPath:         found.TaskList.Path,
		PRDAvailability:      planAvailability(found.PRD.State),
		TaskListAvailability: planAvailability(found.TaskList.State),
		Title:                found.PRD.Title,
		Progress:             PlanProgress{Availability: AvailabilityUnknown},
		ObservedAt:           observedAt,
		TaskListModTime:      found.TaskList.ModTime,
	}
	if read == nil || found.TaskList.Path == "" {
		if found.TaskList.State == planning.StateAbsent {
			plan.Progress.Availability = AvailabilityAbsent
		}
		return plan
	}
	plan.Progress = progressFrom(read(found.TaskList.Path))
	return plan
}

// progressFrom maps the parser's result onto the read model. The mapping keeps
// "malformed" and "unavailable" distinct from a real zero, so a plan that
// could not be understood never renders as no work done.
func progressFrom(parsed tasklist.Plan) PlanProgress {
	progress := PlanProgress{
		Availability:                 planProgressAvailability(parsed.State),
		MilestonesTotal:              parsed.MilestonesTotal,
		MilestonesCompleted:          parsed.MilestonesCompleted,
		SubtasksTotal:                parsed.SubtasksTotal,
		SubtasksCompleted:            parsed.SubtasksCompleted,
		ActiveMilestone:              planItem(parsed.ActiveMilestone),
		NextActionable:               planItem(parsed.NextActionable),
		ImplementationComplete:       parsed.ImplementationComplete,
		DeliveryCheckpointsRemaining: parsed.DeliveryCheckpointsRemaining,
		ParseIssue:                   parsed.ParseIssue,
	}
	for _, checkpoint := range parsed.DeliveryCheckpoints {
		progress.DeliveryCheckpoints = append(progress.DeliveryCheckpoints, planItem(checkpoint))
	}
	if !progress.Availability.OK() {
		// Counts from an unparsed plan are meaningless; drop them rather than
		// let a renderer present them as fact.
		progress.MilestonesTotal, progress.MilestonesCompleted = 0, 0
		progress.SubtasksTotal, progress.SubtasksCompleted = 0, 0
	}
	return progress
}

func planProgressAvailability(state tasklist.PlanState) Availability {
	switch state {
	case tasklist.PlanAvailable:
		return AvailabilityAvailable
	case tasklist.PlanAbsent:
		return AvailabilityAbsent
	case tasklist.PlanMalformed:
		return AvailabilityMalformed
	case tasklist.PlanUnavailable:
		return AvailabilityUnavailable
	default:
		return AvailabilityUnknown
	}
}

func planItem(item tasklist.Item) PlanItem {
	if item.Empty() {
		return PlanItem{}
	}
	return PlanItem{
		Ordinal:    item.Ordinal,
		Text:       item.Text,
		Completed:  item.Completed,
		Checkpoint: item.Checkpoint,
		Line:       item.Line,
	}
}

// planAvailability maps the planning package's state vocabulary onto the read
// model's. The two are deliberately separate types so the planning collector
// does not depend on the snapshot it feeds.
func planAvailability(state planning.State) Availability {
	switch state {
	case planning.StateAvailable:
		return AvailabilityAvailable
	case planning.StateAbsent:
		return AvailabilityAbsent
	case planning.StateMalformed:
		return AvailabilityMalformed
	case planning.StateUnavailable:
		return AvailabilityUnavailable
	default:
		return AvailabilityUnknown
	}
}

const maxJoinedItems = 8

func joinBounded(values []string) string {
	if len(values) > maxJoinedItems {
		values = append(values[:maxJoinedItems:maxJoinedItems], "…")
	}
	joined := ""
	for index, value := range values {
		if index > 0 {
			joined += ", "
		}
		joined += value
	}
	return joined
}
