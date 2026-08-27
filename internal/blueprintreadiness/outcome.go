package blueprintreadiness

// Outcome reports what a recovery action actually did.
//
// It exists because the honest answer is often partial. Installing a plugin
// and enabling it are two operations, and the second can fail after the first
// succeeded. Reporting that as a flat failure would be a lie the user acts on:
// they would try to install again, and the second install would be a no-op
// against a plugin that is already there and still disabled.
//
// So every step the server attempted is named, along with whether it
// completed. The projection that follows describes the world as it now is; the
// outcome describes how it got there.
type Outcome struct {
	// Action is the recovery action that was requested, echoed back so a
	// client rendering several panels can match a result to its request.
	Action Action `json:"action"`
	// Completed reports whether every step the action implies succeeded.
	Completed bool `json:"completed"`
	// Steps are the individual operations, in the order they ran.
	Steps []OutcomeStep `json:"steps,omitempty"`
	// Summary is sanitized display copy stating what happened.
	Summary string `json:"summary,omitempty"`
	// Detail is sanitized supporting copy, typically the next step after a
	// partial result.
	Detail string `json:"detail,omitempty"`
}

// OutcomeStep is one operation inside a recovery action.
type OutcomeStep struct {
	// Name is a stable, closed-set identifier for the operation.
	Name OutcomeStepName `json:"name"`
	// Succeeded reports whether this step completed.
	Succeeded bool `json:"succeeded"`
	// Message is sanitized copy for a failed step. It is display text, never a
	// raw error: a plugin manager error can carry a path or a command line.
	Message string `json:"message,omitempty"`
}

// OutcomeStepName is the closed set of operations a recovery action performs.
type OutcomeStepName string

const (
	// StepPreview resolved the source and built the trust disclosure.
	StepPreview OutcomeStepName = "preview"
	// StepInstall installed the plugin.
	StepInstall OutcomeStepName = "install"
	// StepEnable enabled the plugin.
	StepEnable OutcomeStepName = "enable"
	// StepUpdate replaced the plugin with a newer version.
	StepUpdate OutcomeStepName = "update"
)

var validSteps = map[OutcomeStepName]struct{}{
	StepPreview: {}, StepInstall: {}, StepEnable: {}, StepUpdate: {},
}

// Normalize enforces the outcome's invariants: known action and step names,
// sanitized copy, and Completed that agrees with the steps rather than being
// asserted independently of them.
func (o Outcome) Normalize() Outcome {
	if _, ok := validActions[o.Action]; !ok {
		o.Action = ActionRetry
	}
	steps := make([]OutcomeStep, 0, len(o.Steps))
	everyStepSucceeded := true
	for _, step := range o.Steps {
		if _, ok := validSteps[step.Name]; !ok {
			continue
		}
		step.Message = SanitizeCopy(step.Message, MaxDetailLen)
		if step.Succeeded {
			step.Message = ""
		} else {
			everyStepSucceeded = false
		}
		steps = append(steps, step)
	}
	o.Steps = steps
	if len(steps) == 0 {
		// An outcome with no recorded steps did nothing, whatever it claims.
		o.Completed = false
	} else {
		o.Completed = o.Completed && everyStepSucceeded
	}
	o.Summary = SanitizeCopy(o.Summary, MaxSummaryLen)
	o.Detail = SanitizeCopy(o.Detail, MaxDetailLen)
	return o
}

// NormalizePtr normalizes and returns a pointer, for the common case of
// building an outcome inline into a response body.
func (o *Outcome) NormalizePtr() *Outcome {
	if o == nil {
		return nil
	}
	normalized := o.Normalize()
	return &normalized
}

// Succeeded reports whether a named step ran and completed.
func (o Outcome) Succeeded(name OutcomeStepName) bool {
	for _, step := range o.Steps {
		if step.Name == name {
			return step.Succeeded
		}
	}
	return false
}

// Attempted reports whether a named step ran at all.
func (o Outcome) Attempted(name OutcomeStepName) bool {
	for _, step := range o.Steps {
		if step.Name == name {
			return true
		}
	}
	return false
}
