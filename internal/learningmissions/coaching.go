package learningmissions

// Coaching adapts optional prose only. Criteria never shrink, and brief guided
// support remains guided practice. Unknown/outside help is not independence.
func coachingBudget(p Preferences, policy policyContext, structure StructureID) (string, int) {
	if p.Guidance == GuidanceBasic {
		return "basic", 0
	}
	contract, err := Lookup(structure, DefinitionVersion)
	if err != nil {
		return "full", 0
	}
	competency := map[StructureID]string{DelegateInspect: "delegate", ImproveResult: "improve", RepeatSuccess: "reuse"}[structure]
	if !p.FullGuidance && policy.PracticeKnown && policy.Independent[competency] >= 2 && policy.Independent["evaluate"] >= 2 {
		return "brief", 1
	}
	return "full", len(contract.Steps)
}

func proposalCoaching(data DesignContext, structure StructureID) (string, int, error) {
	contract, err := Lookup(structure, DefinitionVersion)
	if err != nil {
		return "", 0, err
	}
	depth, limit := data.CoachingDepth, data.MaxCoachingHints
	if depth == "" { // trusted older host snapshots, never model-supplied fields
		depth, limit = "full", len(contract.Steps)
		if data.Guidance == GuidanceBasic {
			depth, limit = "basic", 0
		}
	}
	switch depth {
	case "basic":
		if data.Guidance != GuidanceBasic || limit != 0 {
			return "", 0, ErrInvalid
		}
	case "brief":
		if data.Guidance != GuidanceGuided || limit != 1 {
			return "", 0, ErrInvalid
		}
	case "full":
		if data.Guidance != GuidanceGuided || limit < 1 || limit > 4 {
			return "", 0, ErrInvalid
		}
		limit = min(limit, len(contract.Steps))
	default:
		return "", 0, ErrInvalid
	}
	return depth, limit, nil
}
