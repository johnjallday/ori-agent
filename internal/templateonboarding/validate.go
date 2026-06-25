package templateonboarding

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// SupportedVersions are the onboarding spec versions this build understands.
// An unsupported version disables onboarding with an author-facing problem.
var SupportedVersions = map[string]bool{"1": true}

var (
	fieldIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	fieldRefPattern = regexp.MustCompile(`\$\{fields\.([a-zA-Z0-9_]+)\}`)
)

// ValidationResult collects every problem found in a spec so an author-facing
// surface can show them all at once. An empty result means the spec is usable.
// Problems never crash workspace creation — they disable onboarding.
type ValidationResult struct {
	Problems []string
}

// OK reports whether the spec is free of problems.
func (r ValidationResult) OK() bool { return len(r.Problems) == 0 }

// Err joins the problems into a single error, or nil when OK.
func (r ValidationResult) Err() error {
	if r.OK() {
		return nil
	}
	return fmt.Errorf("onboarding spec invalid: %s", strings.Join(r.Problems, "; "))
}

func (r *ValidationResult) addf(format string, args ...any) {
	r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
}

// Validate checks a parsed spec for structural and referential correctness.
func Validate(spec *OnboardingSpec) ValidationResult {
	var res ValidationResult
	if spec == nil {
		res.addf("spec is nil")
		return res
	}
	if !SupportedVersions[strings.TrimSpace(spec.Version)] {
		res.addf("unsupported onboarding version %q (supported: 1)", spec.Version)
	}
	ids := validateFields(spec.Fields, &res)
	validateCompletion(spec.Completion, ids, &res)
	validateDependencies(spec.Dependencies, &res)
	return res
}

// validateFields validates each field and returns the set of valid field IDs,
// used to resolve `${fields.<id>}` references in the completion action.
func validateFields(fields []Field, res *ValidationResult) map[string]bool {
	ids := make(map[string]bool, len(fields))
	for i, f := range fields {
		where := fmt.Sprintf("field[%d]", i)
		if f.ID != "" {
			where = fmt.Sprintf("field %q", f.ID)
		}
		switch {
		case !fieldIDPattern.MatchString(f.ID):
			res.addf("%s: id must match ^[a-z][a-z0-9_]*$", where)
		case ids[f.ID]:
			res.addf("%s: duplicate field id", where)
		default:
			ids[f.ID] = true
		}
		if strings.TrimSpace(f.Label) == "" {
			res.addf("%s: label is required", where)
		}
		switch f.Type {
		case FieldString, FieldNumber, FieldBoolean:
		case FieldEnum:
			if len(f.Options) == 0 {
				res.addf("%s: enum field requires options", where)
			}
		default:
			res.addf("%s: unknown type %q", where, f.Type)
		}
		validateDefault(f, where, res)
		validateFieldValidation(f, where, res)
	}
	return ids
}

func validateDefault(f Field, where string, res *ValidationResult) {
	if f.Default == nil {
		return
	}
	switch f.Type {
	case FieldString:
		if _, ok := f.Default.(string); !ok {
			res.addf("%s: default must be a string", where)
		}
	case FieldNumber:
		if _, ok := f.Default.(float64); !ok {
			res.addf("%s: default must be a number", where)
		}
	case FieldBoolean:
		if _, ok := f.Default.(bool); !ok {
			res.addf("%s: default must be a boolean", where)
		}
	case FieldEnum:
		s, ok := f.Default.(string)
		if !ok {
			res.addf("%s: default must be one of the options", where)
			return
		}
		if !slices.Contains(f.Options, s) {
			res.addf("%s: default %q is not one of the options", where, s)
		}
	}
}

func validateFieldValidation(f Field, where string, res *ValidationResult) {
	v := f.Validation
	if v == nil {
		return
	}
	if v.Min != nil && v.Max != nil && *v.Min > *v.Max {
		res.addf("%s: validation min (%v) is greater than max (%v)", where, *v.Min, *v.Max)
	}
	if v.Pattern != "" {
		if _, err := regexp.Compile(v.Pattern); err != nil {
			res.addf("%s: validation pattern is not a valid regexp: %v", where, err)
		}
	}
}

func validateCompletion(c CompletionAction, fieldIDs map[string]bool, res *ValidationResult) {
	switch c.Type {
	case ActionNone, ActionTask, ActionTool, ActionWorkflowTemplate:
		// All recognized. tool/workflow_template validate but block at execution.
	default:
		res.addf("completion: unknown action type %q", c.Type)
	}
	// ref is required for the reserved external types; optional for none and task
	// (a task may be ad-hoc from instructions alone).
	if (c.Type == ActionTool || c.Type == ActionWorkflowTemplate) && strings.TrimSpace(c.Ref) == "" {
		res.addf("completion: %q action requires a ref", c.Type)
	}
	// Every ${fields.<id>} reference in inputs must resolve to a declared field.
	for key, val := range c.Inputs {
		for _, ref := range fieldRefPattern.FindAllStringSubmatch(val, -1) {
			if id := ref[1]; !fieldIDs[id] {
				res.addf("completion: inputs[%q] references unknown field %q", key, id)
			}
		}
	}
}

func validateDependencies(deps []Dependency, res *ValidationResult) {
	for i, d := range deps {
		switch d.Type {
		case DependencySkill, DependencyMCPServer, DependencyTool, DependencyWorkflowTemplate:
		default:
			res.addf("dependencies[%d]: unknown type %q", i, d.Type)
		}
		if strings.TrimSpace(d.Ref) == "" {
			res.addf("dependencies[%d]: ref is required", i)
		}
	}
}
