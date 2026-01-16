package sessionhttp

import "strings"

const (
	smartInputAutoConfidence    = 0.75
	smartInputLLMAutoConfidence = 0.7
)

var (
	smartInputTaskPrefixes = []string{
		"todo:",
		"todo ",
		"to do:",
		"to do ",
		"task:",
		"task ",
		"remind me",
		"remember to",
	}
	smartInputChatPrefixes = []string{
		"how ",
		"what ",
		"why ",
		"when ",
		"where ",
		"who ",
		"can you",
		"could you",
		"should i",
		"should we",
		"is it",
		"do we",
		"do i",
		"any ideas",
		"thoughts on",
		"help me",
		"explain ",
	}
	smartInputTaskVerbs = map[string]struct{}{
		"create":    {},
		"update":    {},
		"fix":       {},
		"review":    {},
		"draft":     {},
		"write":     {},
		"send":      {},
		"email":     {},
		"call":      {},
		"plan":      {},
		"research":  {},
		"design":    {},
		"build":     {},
		"implement": {},
		"schedule":  {},
		"book":      {},
		"prepare":   {},
		"organize":  {},
	}
)

type smartInputHeuristicResult struct {
	Decision   SmartInputDecision
	Confidence float64
}

func classifySmartInputHeuristic(input string) smartInputHeuristicResult {
	normalized := normalizeSmartInput(input)
	if normalized == "" {
		return smartInputHeuristicResult{
			Decision:   SmartInputDecisionTask,
			Confidence: 0,
		}
	}

	if hasPrefixAny(normalized, smartInputTaskPrefixes) {
		return smartInputHeuristicResult{
			Decision:   SmartInputDecisionTask,
			Confidence: 0.9,
		}
	}

	if strings.Contains(normalized, "?") && hasPrefixAny(normalized, smartInputChatPrefixes) {
		return smartInputHeuristicResult{
			Decision:   SmartInputDecisionChat,
			Confidence: 0.88,
		}
	}

	if strings.Contains(normalized, "?") {
		return smartInputHeuristicResult{
			Decision:   SmartInputDecisionChat,
			Confidence: 0.8,
		}
	}

	if hasPrefixAny(normalized, smartInputChatPrefixes) {
		return smartInputHeuristicResult{
			Decision:   SmartInputDecisionChat,
			Confidence: 0.72,
		}
	}

	if isImperativeTask(normalized) {
		return smartInputHeuristicResult{
			Decision:   SmartInputDecisionTask,
			Confidence: 0.65,
		}
	}

	if strings.Contains(normalized, "please ") {
		return smartInputHeuristicResult{
			Decision:   SmartInputDecisionTask,
			Confidence: 0.6,
		}
	}

	return smartInputHeuristicResult{
		Decision:   SmartInputDecisionTask,
		Confidence: 0.5,
	}
}

func normalizeSmartInput(input string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(input)))
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func hasPrefixAny(input string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(input, prefix) {
			return true
		}
	}
	return false
}

func isImperativeTask(input string) bool {
	first := strings.SplitN(input, " ", 2)[0]
	_, ok := smartInputTaskVerbs[first]
	return ok
}
