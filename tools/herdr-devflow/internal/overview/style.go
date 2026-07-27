package overview

import "strings"

// ANSI select-graphic-rendition codes. Color is always an enhancement here:
// every phase, availability, binding, and severity is also spelled out in
// words, so a reader with color disabled — or with a colour-vision deficiency —
// loses nothing but emphasis.
const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

// palette applies styling only when color is enabled. Holding the decision in
// one place keeps every renderer from re-deriving it.
type palette struct {
	enabled bool
}

func newPalette(options RenderOptions) palette { return palette{enabled: !options.NoColor} }

// paint wraps text in one or more SGR codes. With color disabled it returns
// the text untouched, so a redirected or NO_COLOR run is byte-for-byte plain.
func (p palette) paint(text string, codes ...string) string {
	if !p.enabled || text == "" || len(codes) == 0 {
		return text
	}
	return strings.Join(codes, "") + text + ansiReset
}

// width reports the printed width of a styled string, ignoring escape
// sequences. Column layout must be computed from this rather than from len():
// tabwriter and naive padding both count escapes as visible characters, which
// is what makes colored tables drift out of alignment.
func width(styled string) int {
	visible := 0
	inEscape := false
	for _, r := range styled {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == 0x1b:
			inEscape = true
		default:
			visible++
		}
	}
	return visible
}

// pad right-pads a styled string to a printed width.
func pad(styled string, target int) string {
	if gap := target - width(styled); gap > 0 {
		return styled + strings.Repeat(" ", gap)
	}
	return styled
}

// phase colors track lifecycle attention rather than mood: work that needs a
// human is warm, settled history is quiet.
func (p palette) phase(state PhaseState) string {
	text := phaseCell(state)
	switch state.Phase {
	case PhaseMergedCleanup:
		return p.paint(text, ansiYellow)
	case PhaseReview:
		return p.paint(text, ansiMagenta)
	case PhaseImplementing:
		return p.paint(text, ansiCyan)
	case PhaseReady:
		return p.paint(text, ansiBlue)
	case PhaseShipped:
		return p.paint(text, ansiGreen, ansiDim)
	case PhaseDropped, PhaseUnknown:
		return p.paint(text, ansiDim)
	default:
		return text
	}
}

func (p palette) severity(severity Severity) string {
	switch severity {
	case SeverityError:
		return p.paint(severity.Label(), ansiRed, ansiBold)
	case SeverityWarning:
		return p.paint(severity.Label(), ansiYellow)
	default:
		return p.paint(severity.Label(), ansiDim)
	}
}

// attention is the column a reader scans first, so it carries the strongest
// signal: red for something broken, plain green for genuinely fine.
func (p palette) attention(feature Feature) string {
	text := attentionCell(feature)
	severity, has := feature.Attention()
	if !has {
		return p.paint(text, ansiGreen)
	}
	switch severity {
	case SeverityError:
		return p.paint(text, ansiRed, ansiBold)
	case SeverityWarning:
		return p.paint(text, ansiYellow)
	default:
		return p.paint(text, ansiDim)
	}
}

func (p palette) git(git GitState) string {
	text := gitCell(git)
	switch {
	case git.Availability == AvailabilityUnavailable:
		return p.paint(text, ansiRed)
	case git.Availability == AvailabilityAbsent, git.Availability == AvailabilityUnknown:
		return p.paint(text, ansiDim)
	case git.DivergenceAvailability.OK() && git.Behind > 0:
		return p.paint(text, ansiYellow)
	case git.DirtyAvailability.OK() && git.Dirty:
		return p.paint(text, ansiYellow)
	default:
		return p.paint(text, ansiGreen)
	}
}

func (p palette) remote(remote Remote) string {
	text := remoteCell(remote)
	if remote.Availability == AvailabilityUnavailable {
		return p.paint(text, ansiRed)
	}
	if remote.PullRequest == nil {
		return p.paint(text, ansiDim)
	}
	switch remote.PullRequest.Checks {
	case ChecksFailing:
		return p.paint(text, ansiRed, ansiBold)
	case ChecksPending:
		return p.paint(text, ansiYellow)
	case ChecksPassing:
		return p.paint(text, ansiGreen)
	default:
		return p.paint(text, ansiDim)
	}
}

// agents colors by the weakest binding present: one drifted role is the thing
// worth noticing in a row of otherwise healthy agents.
func (p palette) agents(feature Feature) string {
	text := agentCell(feature)
	if len(feature.Agents) == 0 {
		return p.paint(text, ansiDim)
	}
	switch weakestBinding(feature) {
	case BindingAmbiguous, BindingMissing:
		return p.paint(text, ansiRed)
	case BindingPossibleDrift, BindingUnavailable:
		return p.paint(text, ansiYellow)
	default:
		return p.paint(text, ansiGreen)
	}
}

func (p palette) plan(plan Plan) string {
	return p.planText(planCell(plan), plan)
}

// planCompact bounds the cell for the fixed-width table.
func (p palette) planCompact(plan Plan) string {
	return p.planText(truncate(planCell(plan), maxPlanColumn), plan)
}

func (p palette) planText(text string, plan Plan) string {
	switch {
	case plan.Copy == PlanCopyNone,
		plan.PRDAvailability == AvailabilityAbsent,
		plan.TaskListAvailability == AvailabilityAbsent:
		return p.paint(text, ansiDim)
	case plan.Progress.Availability == AvailabilityMalformed:
		return p.paint(text, ansiRed)
	case plan.Progress.ImplementationComplete:
		return p.paint(text, ansiGreen)
	default:
		return text
	}
}

func (p palette) binding(health BindingHealth) string {
	switch health {
	case BindingExact:
		return p.paint(health.Label(), ansiGreen)
	case BindingPossibleDrift:
		return p.paint(health.Label(), ansiYellow)
	case BindingAmbiguous, BindingMissing:
		return p.paint(health.Label(), ansiRed)
	default:
		return p.paint(health.Label(), ansiDim)
	}
}

func (p palette) header(text string) string { return p.paint(text, ansiBold) }

func (p palette) feature(slug string, terminal bool) string {
	if terminal {
		return p.paint(slug, ansiDim)
	}
	return p.paint(slug, ansiBold)
}

// snapshotStatus makes the single most important fact impossible to miss: an
// incomplete board must not read like a healthy one at a glance.
func (p palette) snapshotStatus(text string, complete bool) string {
	if complete {
		return p.paint(text, ansiGreen)
	}
	return p.paint(text, ansiRed, ansiBold)
}
