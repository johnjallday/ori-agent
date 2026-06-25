package orchestrationhttp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func isFilesystemReadVerificationTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "list_directory", "list_directory_with_sizes", "search_files", "get_file_info", "read_file":
		return true
	default:
		return false
	}
}

func classifyToolAccessBlockedResponse(result string) *workspace.TaskBlockedError {
	normalized := strings.ToLower(strings.TrimSpace(result))
	if normalized == "" {
		return nil
	}

	explicitMarkers := []string{
		"the available tools are limited to",
		"i don't have filesystem browsing tools available in this context",
		"i do not have filesystem browsing tools available in this context",
		"i don't have filesystem access",
		"i do not have filesystem access",
		"cannot explore a general directory",
		"can't explore a general directory",
		"appropriate file-reading tools configured",
		"filesystem access enabled",
		"may not be loaded or configured in the current agent context",
		"may need the appropriate file-reading tools configured",
		"blocked by robots.txt",
		"robots.txt / network restrictions",
		"network restrictions",
		"no html content is available",
		"provide raw html",
		"paste the html",
		"attach a snapshot",
		"alternative data source",
		"access remains blocked",
	}

	if containsAnyExecutionMarker(normalized, explicitMarkers) {
		return buildToolAccessBlockedError(result)
	}

	accessMarkers := []string{
		"i don't have access to",
		"i do not have access to",
		"i don't have",
		"i do not have",
		"i can't access",
		"i cannot access",
		"i'm unable to access",
		"i am unable to access",
	}
	toolMarkers := []string{
		"tool",
		"tools",
		"filesystem",
		"directory",
		"file-reading",
		"weather data",
		"real-time weather",
		"html content",
		"web page",
		"source page",
		"available in this context",
		"agent context",
	}
	unresolvedMarkers := []string{
		"i'd need you to either",
		"you'd need to either",
		"share the directory listing",
		"paste the output of",
		"to complete this task autonomously",
		"to walk you through",
		"neither provides",
		"neither of which can",
		"configured to complete this task",
		"loaded or configured",
		"provide raw html",
		"paste the html",
		"alternative data source",
		"fill in data later",
	}

	if containsAnyExecutionMarker(normalized, accessMarkers) &&
		containsAnyExecutionMarker(normalized, toolMarkers) &&
		containsAnyExecutionMarker(normalized, unresolvedMarkers) {
		return buildToolAccessBlockedError(result)
	}

	return nil
}

func containsAnyExecutionMarker(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func buildToolAccessBlockedError(result string) *workspace.TaskBlockedError {
	return &workspace.TaskBlockedError{
		ReasonCode: "tool_access_unavailable",
		Reason:     "The assigned agent reported that required tools or external access were unavailable for this task.",
		Question:   "This task could not be completed with the tools currently available. Do you want to provide the missing context, retry after enabling the needed tools, or switch agents?",
		SuggestedActions: []string{
			"continue_with_instruction",
			"retry",
			"switch_agent_retry",
			"mark_failed",
		},
		RawResponse: strings.TrimSpace(result),
	}
}

func classifyInvalidTaskCompletionResponse(task workspace.Task, result string) *workspace.TaskBlockedError {
	trimmed := strings.TrimSpace(result)
	normalized := strings.ToLower(trimmed)
	if normalized == "" {
		return nil
	}

	if taskLooksForFreshPublicInformation(task.Description) && responseLooksLikeTaskStatusSummary(normalized) {
		return &workspace.TaskBlockedError{
			ReasonCode: "invalid_status_summary",
			Reason:     "The agent summarized the task status instead of answering the current public-information request.",
			Question:   "Retry this task with web search/source fallback, provide another source, or switch agents?",
			SuggestedActions: []string{
				"retry",
				"continue_with_instruction",
				"switch_agent_retry",
				"mark_failed",
			},
			RawResponse: trimmed,
		}
	}

	if taskLooksForFreshPublicInformation(task.Description) {
		if responseLooksLikeRawToolSummary(normalized) {
			if responseLooksLikeEmptyWebSearchToolSummary(normalized) {
				return &workspace.TaskBlockedError{
					ReasonCode: "empty_web_search_results",
					Reason:     "The web search returned no results and the agent did not broaden the search or synthesize an answer.",
					Question:   "Retry this task with a broader search across public sources?",
					SuggestedActions: []string{
						"retry",
						"continue_with_instruction",
						"switch_agent_retry",
						"mark_failed",
					},
					RawResponse: trimmed,
				}
			}
			return &workspace.TaskBlockedError{
				ReasonCode: "tool_only_result",
				Reason:     "The agent returned raw tool output instead of a final answer.",
				Question:   "Retry this task and require the agent to synthesize the tool result into an answer?",
				SuggestedActions: []string{
					"retry",
					"continue_with_instruction",
					"switch_agent_retry",
					"mark_failed",
				},
				RawResponse: trimmed,
			}
		}

		if reason := publicInfoLocationMismatchReason(task.Description, normalized); reason != "" {
			return &workspace.TaskBlockedError{
				ReasonCode: "location_mismatch",
				Reason:     reason,
				Question:   "Retry with web search first and only use sources that match the requested location?",
				SuggestedActions: []string{
					"retry",
					"continue_with_instruction",
					"switch_agent_retry",
					"mark_failed",
				},
				RawResponse: trimmed,
			}
		}
	}

	if taskAllowsPlaceholderOutput(task.Description) {
		return nil
	}

	if responseLooksLikePlaceholderResult(normalized) {
		return &workspace.TaskBlockedError{
			ReasonCode: "placeholder_result",
			Reason:     "The task returned a placeholder result instead of the requested answer.",
			Question:   "Retry this task, provide missing source content, or switch agents?",
			SuggestedActions: []string{
				"retry",
				"continue_with_instruction",
				"switch_agent_retry",
				"mark_failed",
			},
			RawResponse: trimmed,
		}
	}

	return nil
}

func taskLooksForFreshPublicInformation(description string) bool {
	normalized := strings.ToLower(strings.TrimSpace(description))
	if normalized == "" {
		return false
	}

	markers := []string{
		"today",
		"current",
		"latest",
		"recent",
		"now",
		"weather",
		"forecast",
		"pollen",
		"air quality",
		"price",
		"stock",
		"score",
		"news",
		"flight",
		"hotel",
	}
	return containsAnyExecutionMarker(normalized, markers)
}

func responseLooksLikeTaskStatusSummary(normalized string) bool {
	markers := []string{
		"current status for task",
		"status: in_progress",
		"status: completed",
		"what happened so far",
		"what you'll likely want next",
	}
	return containsAnyExecutionMarker(normalized, markers) &&
		(strings.Contains(normalized, "task ") || strings.Contains(normalized, "status:"))
}

func responseLooksLikeRawToolSummary(normalized string) bool {
	return strings.HasPrefix(strings.TrimSpace(normalized), "tool results:")
}

func responseLooksLikeEmptyWebSearchToolSummary(normalized string) bool {
	if !strings.Contains(normalized, "web_search") {
		return false
	}
	compacted := strings.NewReplacer(" ", "", "\n", "", "\t", "", "\r", "").Replace(normalized)
	return strings.Contains(compacted, `"results":[]`) ||
		strings.Contains(compacted, `"results":null`) ||
		strings.Contains(normalized, "no search results") ||
		strings.Contains(normalized, "no results found")
}

func publicInfoLocationMismatchReason(description, normalizedResult string) string {
	if strings.Contains(normalizedResult, "no locations found") {
		return "The source page did not resolve the requested location."
	}

	requestedZip := firstFiveDigitToken(description)
	resultZip := firstFiveDigitToken(normalizedResult)
	if requestedZip != "" && resultZip != "" && requestedZip != resultZip {
		return fmt.Sprintf("The source page used ZIP %s, but the task requested ZIP %s.", resultZip, requestedZip)
	}

	normalizedDescription := strings.ToLower(strings.TrimSpace(description))
	if requestsNYCLocation(normalizedDescription) && resultMentionsNonNYCLocation(normalizedResult) {
		return "The source page appears to be for Austin, TX, but the task requested NYC."
	}

	return ""
}

func firstFiveDigitToken(value string) string {
	re := regexp.MustCompile(`\b\d{5}\b`)
	return re.FindString(value)
}

func requestsNYCLocation(normalizedDescription string) bool {
	return strings.Contains(normalizedDescription, "nyc") ||
		strings.Contains(normalizedDescription, "new york city") ||
		strings.Contains(normalizedDescription, "new york, ny") ||
		strings.Contains(normalizedDescription, "new york")
}

func resultMentionsNonNYCLocation(normalizedResult string) bool {
	return strings.Contains(normalizedResult, "austin, tx") ||
		strings.Contains(normalizedResult, "austin tx") ||
		strings.Contains(normalizedResult, "/73344") ||
		strings.Contains(normalizedResult, "(73344)")
}

func taskAllowsPlaceholderOutput(description string) bool {
	normalized := strings.ToLower(strings.TrimSpace(description))
	if normalized == "" {
		return false
	}
	markers := []string{
		"placeholder",
		"template",
		"draft",
		"boilerplate",
		"tbd",
	}
	return containsAnyExecutionMarker(normalized, markers)
}

func responseLooksLikePlaceholderResult(normalized string) bool {
	if strings.Contains(normalized, "fill in data later") || strings.Contains(normalized, "fill in later") {
		return true
	}
	if strings.Contains(normalized, "placeholder") && (strings.Contains(normalized, "tbd") || strings.Contains(normalized, "...")) {
		return true
	}
	if strings.Contains(normalized, "|") && (strings.Contains(normalized, "| tbd") || strings.Contains(normalized, " tbd |") || strings.Contains(normalized, "| ...") || strings.Contains(normalized, " ... |")) {
		return true
	}
	return false
}

func classifyFilesystemListingVerificationFailure(task workspace.Task, result string, evidence taskExecutionEvidence) *workspace.TaskBlockedError {
	if !workspace.IsReadOnlyFilesystemListingIntent(task.Description) {
		return nil
	}
	if len(evidence.SuccessfulFilesystemReadToolNames) > 0 {
		return nil
	}

	return &workspace.TaskBlockedError{
		ReasonCode: "filesystem_result_unverified",
		Reason:     "Task returned a filesystem listing answer without successful filesystem verification",
		Question:   "I need to verify the folder contents with filesystem tools before completing this task. Retry with explicit filesystem verification?",
		SuggestedActions: []string{
			"retry",
			"switch_agent_retry",
			"continue_with_instruction",
			"mark_failed",
		},
		RawResponse: strings.TrimSpace(result),
	}
}

func classifyFilesystemListingIncompleteResponse(task workspace.Task, result string) *workspace.TaskBlockedError {
	if !workspace.IsReadOnlyFilesystemListingIntent(task.Description) {
		return nil
	}
	if filesystemListingAnswerLooksComplete(result) {
		return nil
	}

	return &workspace.TaskBlockedError{
		ReasonCode: "filesystem_listing_incomplete",
		Reason:     "Task did not return the requested filesystem file list",
		Question:   "I need to return the actual file list, not a follow-up offer. Retry and return the verified list directly?",
		SuggestedActions: []string{
			"retry",
			"switch_agent_retry",
			"continue_with_instruction",
			"mark_failed",
		},
		RawResponse: strings.TrimSpace(result),
	}
}

func filesystemListingAnswerLooksComplete(result string) bool {
	normalized := strings.ToLower(strings.TrimSpace(result))
	if normalized == "" {
		return false
	}

	emptyMarkers := []string{
		"folder is empty",
		"directory is empty",
		"contains no files",
		"no files found",
		"there are no files",
		"empty folder",
		"empty directory",
	}
	for _, marker := range emptyMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}

	return responseContainsFilenameLikeEntry(result)
}

func responseContainsFilenameLikeEntry(result string) bool {
	for _, line := range strings.Split(result, "\n") {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "-*0123456789.) \t"))
		if trimmed == "" {
			continue
		}
		for _, token := range strings.Fields(trimmed) {
			cleaned := strings.Trim(token, "\"'`,;:()[]{}")
			if looksLikeFilenameToken(cleaned) {
				return true
			}
		}
	}
	return false
}

func looksLikeFilenameToken(token string) bool {
	dot := strings.LastIndex(token, ".")
	if dot <= 0 || dot >= len(token)-1 {
		return false
	}

	ext := token[dot+1:]
	if len(ext) > 8 {
		return false
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func blockedExecutionSummary(blockedErr *workspace.TaskBlockedError) string {
	if blockedErr == nil {
		return ""
	}
	if raw := strings.TrimSpace(blockedErr.RawResponse); raw != "" {
		return raw
	}
	if reason := strings.TrimSpace(blockedErr.Reason); reason != "" {
		if question := strings.TrimSpace(blockedErr.Question); question != "" {
			return reason + "\n\n" + question
		}
		return reason
	}
	return blockedErr.Error()
}

func responseNeedsUserInput(result string) bool {
	normalized := strings.ToLower(strings.TrimSpace(result))
	if normalized == "" {
		return true
	}

	highConfidenceMarkers := []string{
		"i need clarification",
		"need clarification to complete this task",
		"please provide these details",
		"before i can complete this task",
		"i need more information",
		"awaiting your input",
		"could you please confirm",
		"please confirm if",
		"provide additional directions",
		"recommended next steps",
		"choose one",
		"choose an option",
		"choose one of the following",
		"tell me which option",
		"which option to take",
		"just say",
		"what would you like me to do next",
		"how you'd like to proceed",
		"how you’d like to proceed",
		"like to proceed",
	}
	for _, marker := range highConfidenceMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}

	softMarkers := []string{
		"could you clarify",
		"please clarify",
		"please confirm",
		"which location",
		"what specific",
		"how should i proceed",
		"what format",
		"i don't have direct access",
		"i do not have direct access",
		"located somewhere else",
	}

	if strings.Contains(normalized, "option a") && strings.Contains(normalized, "option b") {
		return true
	}

	matches := 0
	for _, marker := range softMarkers {
		if strings.Contains(normalized, marker) {
			matches++
		}
	}

	questionMarks := strings.Count(result, "?")
	if matches >= 2 && questionMarks >= 1 {
		return true
	}

	if strings.Contains(normalized, "1.") &&
		strings.Contains(normalized, "2.") &&
		questionMarks >= 2 &&
		(matches >= 1 || strings.Contains(normalized, "however")) {
		return true
	}

	return false
}

func extractClarificationQuestion(result string) string {
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "?") && len(trimmed) <= 200 {
			return trimmed
		}
	}
	return "I still need confirmation to continue. Should I retry, switch agents, or proceed with your guidance?"
}

func extractClarificationWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	if step := extractQuestionBlockWorkflowStep(result); step != nil {
		return step
	}
	if step := extractEnumeratedClarificationWorkflowStep(result); step != nil {
		return step
	}
	if step := extractInlineClarificationWorkflowStep(result); step != nil {
		return step
	}
	if step := extractQuestionFormWorkflowStep(result); step != nil {
		return step
	}
	return nil
}

func extractQuestionBlockWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	blocks := extractClarificationQuestionBlocks(result)
	if len(blocks) == 0 {
		return nil
	}

	fields := make([]workspace.TaskBlockedField, 0, len(blocks))
	for index, block := range blocks {
		field, ok := buildClarificationField(block.Question, index)
		if !ok {
			continue
		}

		field.Type = "select"
		field.Options = block.Options
		field.Description = strings.TrimSpace(block.Question)
		field.Evidence = deriveClarificationQuestionEvidence(block.Options)
		fields = append(fields, field)
	}

	if len(fields) == 0 {
		return nil
	}

	return &workspace.TaskBlockedWorkflowStep{
		StepType:        "ask_form",
		Title:           "Provide the missing details",
		Summary:         "Answer the questions below so the task can continue.",
		Fields:          fields,
		FreeTextAllowed: true,
	}
}

func extractEnumeratedClarificationWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	lines := strings.Split(result, "\n")
	choices := make([]workspace.TaskBlockedChoice, 0, 4)
	started := false

	for _, line := range lines {
		match := blockedEnumeratedChoicePattern.FindStringSubmatch(line)
		if len(match) == 5 {
			number := strings.TrimSpace(firstNonEmptyString(match[1], match[2], match[3]))
			number = strings.ToUpper(number)
			label := cleanBlockedChoiceText(match[4])
			if label == "" {
				continue
			}
			choices = append(choices, workspace.TaskBlockedChoice{
				ID:     buildBlockedChoiceID(number, label),
				Label:  label,
				Number: number,
			})
			started = true
			if len(choices) >= 5 {
				break
			}
			continue
		}

		if !started {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		break
	}

	if len(choices) < 2 {
		return nil
	}
	markRecommendedBlockedChoices(result, choices)

	return &workspace.TaskBlockedWorkflowStep{
		StepType:        "ask_choice",
		Title:           "Choose the next step",
		Summary:         "Pick one option below to continue this task.",
		Choices:         choices,
		FreeTextAllowed: true,
	}
}

func extractInlineClarificationWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	lines := strings.Split(result, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, "?") {
			continue
		}
		for _, pattern := range blockedInlineChoicePatterns {
			match := pattern.FindStringSubmatch(line)
			if len(match) != 3 {
				continue
			}

			first := cleanBlockedChoiceText(match[1])
			second := cleanBlockedChoiceText(match[2])
			if first == "" || second == "" || strings.EqualFold(first, second) {
				continue
			}

			return &workspace.TaskBlockedWorkflowStep{
				StepType: "ask_choice",
				Title:    "Choose the next step",
				Summary:  "Pick one option below to continue this task.",
				Choices: []workspace.TaskBlockedChoice{
					{
						ID:     buildBlockedChoiceID("1", first),
						Label:  first,
						Number: "1",
					},
					{
						ID:     buildBlockedChoiceID("2", second),
						Label:  second,
						Number: "2",
					},
				},
				FreeTextAllowed: true,
			}
		}
	}
	return nil
}

func markRecommendedBlockedChoices(result string, choices []workspace.TaskBlockedChoice) {
	recommendedNumbers := extractRecommendedChoiceNumbers(result)
	for index := range choices {
		label, labelRecommended := stripRecommendedChoiceMarker(choices[index].Label)
		if label != "" {
			choices[index].Label = label
		}
		number := strings.ToUpper(strings.TrimSpace(choices[index].Number))
		if labelRecommended || recommendedNumbers[number] {
			choices[index].Recommended = true
		}
	}
}

func extractRecommendedChoiceNumbers(result string) map[string]bool {
	recommended := map[string]bool{}
	lines := strings.Split(result, "\n")
	optionPattern := regexp.MustCompile(`(?i)\boption\s+([a-z0-9]+)\b|\b([a-z])[.)]\b`)
	for _, line := range lines {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "recommend") && !strings.Contains(lower, "by default") {
			continue
		}
		for _, match := range optionPattern.FindAllStringSubmatch(line, -1) {
			if len(match) < 3 {
				continue
			}
			number := strings.ToUpper(strings.TrimSpace(firstNonEmptyString(match[1], match[2])))
			if number != "" {
				recommended[number] = true
			}
		}
	}
	return recommended
}

func stripRecommendedChoiceMarker(label string) (string, bool) {
	cleaned := cleanBlockedChoiceText(label)
	if cleaned == "" {
		return "", false
	}
	recommendedPattern := regexp.MustCompile(`(?i)\s*[\[(]recommended[\])]\s*`)
	recommended := recommendedPattern.MatchString(cleaned)
	cleaned = recommendedPattern.ReplaceAllString(cleaned, " ")
	cleaned = cleanBlockedChoiceText(cleaned)
	return cleaned, recommended
}

func extractClarificationQuestionBlocks(result string) []clarificationQuestionBlock {
	lines := strings.Split(result, "\n")
	blocks := make([]clarificationQuestionBlock, 0, 4)

	for i := 0; i < len(lines); {
		match := blockedQuestionPromptPattern.FindStringSubmatch(lines[i])
		if len(match) != 3 {
			i++
			continue
		}

		question := cleanBlockedChoiceText(match[2])
		if question == "" {
			i++
			continue
		}

		options := make([]workspace.TaskBlockedFieldOption, 0, 4)
		j := i + 1
		for ; j < len(lines); j++ {
			rawLine := lines[j]
			if blockedQuestionPromptPattern.MatchString(rawLine) {
				break
			}

			optionMatch := blockedLetteredOptionPattern.FindStringSubmatch(rawLine)
			if len(optionMatch) == 3 {
				label := cleanBlockedChoiceText(optionMatch[2])
				if label == "" {
					continue
				}
				label, evidence := splitClarificationOptionEvidence(label)
				options = append(options, workspace.TaskBlockedFieldOption{
					Value:       label,
					Label:       label,
					Description: evidence,
				})
				continue
			}

			trimmed := strings.TrimSpace(rawLine)
			if trimmed == "" {
				continue
			}
			if len(options) == 0 {
				break
			}

			continuation := cleanBlockedChoiceText(trimmed)
			if continuation == "" {
				continue
			}
			lastIndex := len(options) - 1
			options[lastIndex].Label = strings.TrimSpace(options[lastIndex].Label + " " + continuation)
			options[lastIndex].Label, options[lastIndex].Description = splitClarificationOptionEvidence(options[lastIndex].Label)
			options[lastIndex].Value = options[lastIndex].Label
		}

		if len(options) >= 2 {
			if !strings.HasSuffix(question, "?") {
				question += "?"
			}
			blocks = append(blocks, clarificationQuestionBlock{
				Question: question,
				Options:  options,
			})
			i = j
			continue
		}

		i++
	}

	return blocks
}

func splitClarificationOptionEvidence(label string) (string, string) {
	cleaned := cleanBlockedChoiceText(label)
	if cleaned == "" {
		return "", ""
	}

	start := strings.LastIndex(cleaned, "(")
	end := strings.LastIndex(cleaned, ")")
	if start >= 0 && end > start+1 && end == len(cleaned)-1 {
		mainLabel := cleanBlockedChoiceText(cleaned[:start])
		evidence := cleanBlockedChoiceText(cleaned[start+1 : end])
		if mainLabel != "" && evidence != "" {
			return mainLabel, ensureClarificationSentence(evidence)
		}
	}

	return cleaned, ""
}

func deriveClarificationQuestionEvidence(options []workspace.TaskBlockedFieldOption) string {
	if len(options) == 0 {
		return ""
	}

	seen := make(map[string]struct{}, len(options))
	evidence := make([]string, 0, 2)
	for _, option := range options {
		description := strings.TrimSpace(option.Description)
		if description == "" {
			continue
		}
		key := strings.ToLower(description)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		evidence = append(evidence, ensureClarificationSentence(description))
		if len(evidence) >= 2 {
			break
		}
	}

	return strings.TrimSpace(strings.Join(evidence, " "))
}

func ensureClarificationSentence(value string) string {
	cleaned := cleanBlockedChoiceText(value)
	if cleaned == "" {
		return ""
	}
	if strings.HasSuffix(cleaned, ".") || strings.HasSuffix(cleaned, "!") || strings.HasSuffix(cleaned, "?") {
		return cleaned
	}
	return cleaned + "."
}

func extractQuestionFormWorkflowStep(result string) *workspace.TaskBlockedWorkflowStep {
	questions := extractClarificationQuestions(result)
	if len(questions) == 0 {
		return nil
	}

	fields := make([]workspace.TaskBlockedField, 0, len(questions))
	for index, question := range questions {
		field, ok := buildClarificationField(question, index)
		if !ok {
			continue
		}
		fields = append(fields, field)
	}

	if len(fields) == 0 {
		return nil
	}

	if len(fields) == 1 && !fieldHasExplicitOptions(fields[0]) {
		lowerQuestion := strings.ToLower(strings.TrimSpace(questions[0]))
		if lowerQuestion == "" ||
			strings.Contains(lowerQuestion, "how should i proceed") ||
			strings.Contains(lowerQuestion, "should i retry") {
			return nil
		}
	}

	return &workspace.TaskBlockedWorkflowStep{
		StepType:        "ask_form",
		Title:           "Provide the missing details",
		Summary:         "Answer the questions below so the task can continue.",
		Fields:          fields,
		FreeTextAllowed: true,
	}
}
