package chathttp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// UtilityRouteMode describes intake routing mode before full chat/planner flow.
type UtilityRouteMode string

const (
	UtilityRouteDirect    UtilityRouteMode = "utility_direct"
	UtilityRouteWorkspace UtilityRouteMode = "workspace_task"
	UtilityRouteScratch   UtilityRouteMode = "scratch_task"
	UtilityRouteSpecial   UtilityRouteMode = "specialist_handoff"
)

// UtilityRouteDecision is the normalized output of intake classification.
type UtilityRouteDecision struct {
	Mode     UtilityRouteMode
	ToolName string
	ToolArgs string
	Reason   string
}

var (
	urlPattern = regexp.MustCompile(`https?://[^\s]+`)
	ianaTZRe   = regexp.MustCompile(`\b[A-Za-z_]+/[A-Za-z_]+\b`)
)

var commonCityTimezones = map[string]string{
	"tokyo":         "Asia/Tokyo",
	"seoul":         "Asia/Seoul",
	"new york":      "America/New_York",
	"nyc":           "America/New_York",
	"los angeles":   "America/Los_Angeles",
	"san francisco": "America/Los_Angeles",
	"london":        "Europe/London",
	"paris":         "Europe/Paris",
	"berlin":        "Europe/Berlin",
	"sydney":        "Australia/Sydney",
	"singapore":     "Asia/Singapore",
	"hong kong":     "Asia/Hong_Kong",
	"delhi":         "Asia/Kolkata",
	"mumbai":        "Asia/Kolkata",
}

// classifyUtilityRoute classifies prompt into utility/workspace/scratch/specialist modes.
func classifyUtilityRoute(prompt string) UtilityRouteDecision {
	text := strings.TrimSpace(prompt)
	if text == "" {
		return UtilityRouteDecision{}
	}
	lower := strings.ToLower(text)

	if decision, ok := classifyBrowserRoute(text, lower); ok {
		return decision
	}
	if decision, ok := classifyWebFetchRoute(text, lower); ok {
		return decision
	}
	if decision, ok := classifyWebSearchRoute(text, lower); ok {
		return decision
	}
	if decision, ok := classifyTimeRoute(text, lower); ok {
		return decision
	}
	if decision, ok := classifyWeatherRoute(text, lower); ok {
		return decision
	}
	if decision, ok := classifyAirQualityRoute(text, lower); ok {
		return decision
	}

	if looksLikeScratchTask(lower) {
		return UtilityRouteDecision{
			Mode:   UtilityRouteScratch,
			Reason: "prompt indicates disposable scratch execution",
		}
	}
	if looksLikeWorkspaceTask(lower) {
		return UtilityRouteDecision{
			Mode:   UtilityRouteWorkspace,
			Reason: "prompt indicates workspace-scoped execution",
		}
	}
	if looksLikeSpecialistTask(lower) {
		return UtilityRouteDecision{
			Mode:   UtilityRouteSpecial,
			Reason: "prompt indicates specialist handoff",
		}
	}
	return UtilityRouteDecision{}
}

func classifyTimeRoute(original, lower string) (UtilityRouteDecision, bool) {
	if !containsAny(lower, []string{"time", "timezone", "clock"}) {
		return UtilityRouteDecision{}, false
	}

	tz := inferTimezoneFromPrompt(original)
	args, _ := json.Marshal(TimeRequest{Timezone: tz})
	return UtilityRouteDecision{
		Mode:     UtilityRouteDirect,
		ToolName: "time",
		ToolArgs: string(args),
		Reason:   "matched time utility intent",
	}, true
}

func classifyWeatherRoute(original, lower string) (UtilityRouteDecision, bool) {
	if !containsAny(lower, []string{"weather", "forecast", "temperature"}) {
		return UtilityRouteDecision{}, false
	}

	location := inferLocationForWeather(original)
	if strings.TrimSpace(location) == "" {
		return UtilityRouteDecision{}, false
	}

	args, _ := json.Marshal(WeatherRequest{
		Location: location,
		Units:    inferWeatherUnits(lower),
	})
	return UtilityRouteDecision{
		Mode:     UtilityRouteDirect,
		ToolName: "weather",
		ToolArgs: string(args),
		Reason:   "matched weather utility intent",
	}, true
}

func classifyAirQualityRoute(original, lower string) (UtilityRouteDecision, bool) {
	if !looksLikeAirQualityPrompt(lower) {
		return UtilityRouteDecision{}, false
	}

	location := inferLocationForAirQuality(original)
	if strings.TrimSpace(location) == "" {
		return UtilityRouteDecision{}, false
	}

	args, _ := json.Marshal(AirQualityRequest{
		Location: location,
		Standard: inferAirQualityStandard(lower),
	})
	return UtilityRouteDecision{
		Mode:     UtilityRouteDirect,
		ToolName: "air_quality",
		ToolArgs: string(args),
		Reason:   "matched air quality utility intent",
	}, true
}

func classifyWebSearchRoute(original, lower string) (UtilityRouteDecision, bool) {
	if !looksLikeWebSearchPrompt(lower) {
		return UtilityRouteDecision{}, false
	}
	query := inferSearchQuery(original)
	if strings.TrimSpace(query) == "" {
		query = original
	}
	args, _ := json.Marshal(WebSearchRequest{Query: strings.TrimSpace(query)})
	return UtilityRouteDecision{
		Mode:     UtilityRouteDirect,
		ToolName: "web_search",
		ToolArgs: string(args),
		Reason:   "matched web search utility intent",
	}, true
}

func classifyWebFetchRoute(original, lower string) (UtilityRouteDecision, bool) {
	urlMatch := urlPattern.FindString(original)
	if strings.TrimSpace(urlMatch) == "" {
		return UtilityRouteDecision{}, false
	}
	if !containsAny(lower, []string{"fetch", "read", "open", "summarize", "extract", "webpage", "web page", "url"}) {
		return UtilityRouteDecision{}, false
	}

	args, _ := json.Marshal(WebFetchRequest{URL: urlMatch})
	return UtilityRouteDecision{
		Mode:     UtilityRouteDirect,
		ToolName: "web_fetch",
		ToolArgs: string(args),
		Reason:   "matched web fetch utility intent",
	}, true
}

func classifyBrowserRoute(original, lower string) (UtilityRouteDecision, bool) {
	switch {
	case strings.HasPrefix(lower, "browser open "):
		url := strings.TrimSpace(strings.TrimPrefix(original, "browser open "))
		if strings.TrimSpace(url) == "" {
			return UtilityRouteDecision{}, false
		}
		args, _ := json.Marshal(BrowserRequest{Action: "open_url", URL: normalizeBrowserOpenTargetURL(url)})
		return UtilityRouteDecision{
			Mode:     UtilityRouteDirect,
			ToolName: "browser",
			ToolArgs: string(args),
			Reason:   "matched explicit browser open command",
		}, true
	case strings.HasPrefix(lower, "browser click "):
		sel := strings.TrimSpace(strings.TrimPrefix(original, "browser click "))
		if strings.TrimSpace(sel) == "" {
			return UtilityRouteDecision{}, false
		}
		args, _ := json.Marshal(BrowserRequest{Action: "click", Selector: sel})
		return UtilityRouteDecision{
			Mode:     UtilityRouteDirect,
			ToolName: "browser",
			ToolArgs: string(args),
			Reason:   "matched explicit browser click command",
		}, true
	case strings.HasPrefix(lower, "browser extract "):
		sel := strings.TrimSpace(strings.TrimPrefix(original, "browser extract "))
		if strings.TrimSpace(sel) == "" {
			return UtilityRouteDecision{}, false
		}
		args, _ := json.Marshal(BrowserRequest{Action: "extract_text", Selector: sel})
		return UtilityRouteDecision{
			Mode:     UtilityRouteDirect,
			ToolName: "browser",
			ToolArgs: string(args),
			Reason:   "matched explicit browser extract command",
		}, true
	}

	// Convenience: "open https://..." routes to browser open_url.
	if strings.HasPrefix(lower, "open http://") || strings.HasPrefix(lower, "open https://") {
		raw := strings.TrimSpace(strings.TrimPrefix(original, "open "))
		args, _ := json.Marshal(BrowserRequest{Action: "open_url", URL: normalizeBrowserOpenTargetURL(raw)})
		return UtilityRouteDecision{
			Mode:     UtilityRouteDirect,
			ToolName: "browser",
			ToolArgs: string(args),
			Reason:   "matched open-url browser intent",
		}, true
	}

	// Convenience: "open youtube.com" routes to browser open_url.
	if strings.HasPrefix(lower, "open ") {
		raw := strings.TrimSpace(strings.TrimPrefix(original, "open "))
		if looksLikeWebHostTarget(raw) {
			args, _ := json.Marshal(BrowserRequest{Action: "open_url", URL: normalizeBrowserOpenTargetURL(raw)})
			return UtilityRouteDecision{
				Mode:     UtilityRouteDirect,
				ToolName: "browser",
				ToolArgs: string(args),
				Reason:   "matched open-domain browser intent",
			}, true
		}
	}
	return UtilityRouteDecision{}, false
}

func looksLikeWebHostTarget(raw string) bool {
	candidate := strings.TrimSpace(strings.Trim(raw, " \t\r\n\"'`.,!?;:()[]{}"))
	if candidate == "" {
		return false
	}
	if strings.Contains(candidate, " ") || strings.Contains(candidate, "@") {
		return false
	}
	if strings.Contains(candidate, "://") || strings.HasPrefix(strings.ToLower(candidate), "www.") {
		return true
	}

	host := candidate
	if idx := strings.IndexAny(host, "/?#"); idx >= 0 {
		host = host[:idx]
	}
	if host == "" || !strings.Contains(host, ".") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func normalizeBrowserOpenTargetURL(raw string) string {
	candidate := strings.TrimSpace(strings.Trim(raw, " \t\r\n\"'`"))
	if candidate == "" {
		return ""
	}
	lower := strings.ToLower(candidate)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "file://") {
		return candidate
	}
	return "https://" + candidate
}

func inferTimezoneFromPrompt(prompt string) string {
	if match := ianaTZRe.FindString(prompt); strings.TrimSpace(match) != "" {
		return strings.TrimSpace(match)
	}
	lower := strings.ToLower(prompt)
	for city, tz := range commonCityTimezones {
		if strings.Contains(lower, city) {
			return tz
		}
	}
	return "UTC"
}

func inferLocationForWeather(prompt string) string {
	lower := strings.ToLower(prompt)
	markers := []string{
		"weather in ", "weather for ", "forecast in ", "forecast for ", "temperature in ", "temperature for ",
	}
	for _, marker := range markers {
		if idx := strings.Index(lower, marker); idx >= 0 {
			loc := strings.TrimSpace(prompt[idx+len(marker):])
			loc = normalizeInferredLocation(loc)
			if loc != "" {
				return loc
			}
		}
	}
	// Handle "<location> weather"
	if idx := strings.Index(lower, " weather"); idx > 0 {
		loc := strings.TrimSpace(prompt[:idx])
		loc = normalizeInferredLocation(loc)
		if loc != "" {
			return loc
		}
	}
	return ""
}

func inferLocationForAirQuality(prompt string) string {
	lower := strings.ToLower(prompt)
	markers := []string{
		"air quality in ", "air quality for ", "aqi in ", "aqi for ", "pollution in ", "pollution for ", "pm2.5 in ", "pm10 in ",
	}
	for _, marker := range markers {
		if idx := strings.Index(lower, marker); idx >= 0 {
			loc := strings.TrimSpace(prompt[idx+len(marker):])
			loc = normalizeInferredLocation(loc)
			if loc != "" {
				return loc
			}
		}
	}
	if idx := strings.Index(lower, " air quality"); idx > 0 {
		loc := strings.TrimSpace(prompt[:idx])
		loc = normalizeInferredLocation(loc)
		if loc != "" {
			return loc
		}
	}
	if idx := strings.Index(lower, " aqi"); idx > 0 {
		loc := strings.TrimSpace(prompt[:idx])
		loc = normalizeInferredLocation(loc)
		if loc != "" {
			return loc
		}
	}
	return ""
}

func inferWeatherUnits(lower string) string {
	if containsAny(lower, []string{"fahrenheit", "f ", " f)", " f."}) {
		return "fahrenheit"
	}
	return "celsius"
}

func inferAirQualityStandard(lower string) string {
	if containsAny(lower, []string{"eu aqi", "european aqi", "european scale", "eu scale"}) {
		return "eu"
	}
	return "us"
}

func inferSearchQuery(prompt string) string {
	query := strings.TrimSpace(prompt)
	replacers := []string{
		"search the web for",
		"search web for",
		"web search for",
		"search online for",
		"search online",
		"search the web",
		"look up",
		"lookup",
	}
	lower := strings.ToLower(query)
	for _, marker := range replacers {
		if strings.HasPrefix(lower, marker) {
			return strings.TrimSpace(query[len(marker):])
		}
	}
	return query
}

func looksLikeWebSearchPrompt(lower string) bool {
	if containsAny(lower, []string{"search the web", "web search", "search online", "internet search"}) {
		return true
	}
	if containsAny(lower, []string{"search", "look up", "lookup", "find"}) && containsAny(lower, []string{"web", "online", "internet"}) {
		return true
	}
	if containsAny(lower, []string{"latest news", "look up", "lookup"}) && containsAny(lower, []string{"web", "online", "internet"}) {
		return true
	}
	return false
}

func looksLikeAirQualityPrompt(lower string) bool {
	return containsAny(lower, []string{
		"air quality", "aqi", "pm2.5", "pm2_5", "pm10", "pollution level", "pollution",
	})
}

func normalizeInferredLocation(raw string) string {
	loc := strings.TrimSpace(raw)
	loc = strings.Trim(loc, " .?!,")
	if loc == "" {
		return ""
	}

	lower := strings.ToLower(loc)
	suffixes := []string{
		" right now",
		" currently",
		" today",
		" now",
		" tonight",
		" tomorrow",
		" this morning",
		" this afternoon",
		" this evening",
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) {
			loc = strings.TrimSpace(loc[:len(loc)-len(suffix)])
			lower = strings.ToLower(loc)
			loc = strings.Trim(loc, " .?!,")
		}
	}
	return strings.Trim(loc, " .?!,")
}

func looksLikeWorkspaceTask(lower string) bool {
	return containsAny(lower, []string{
		"code", "repo", "repository", "file", "files", "test", "build", "compile", "refactor", "workspace",
	})
}

func looksLikeScratchTask(lower string) bool {
	return containsAny(lower, []string{
		"scratch", "temporary", "temp workspace", "throwaway", "sandbox",
	})
}

func looksLikeSpecialistTask(lower string) bool {
	return containsAny(lower, []string{
		"high risk", "compliance", "legal", "medical", "specialist", "delegate",
	})
}

func containsAny(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func formatUtilityDirectResponse(toolName, rawResult string) string {
	switch toolName {
	case "time":
		var payload TimeResponse
		if json.Unmarshal([]byte(rawResult), &payload) == nil && payload.LocalTime != "" {
			return fmt.Sprintf("The current time in %s is %s.", payload.Timezone, payload.LocalTime)
		}
	case "weather":
		var payload WeatherResponse
		if json.Unmarshal([]byte(rawResult), &payload) == nil && payload.Location != "" {
			unitSymbol := "C"
			if strings.EqualFold(payload.Units, "fahrenheit") {
				unitSymbol = "F"
			}
			return fmt.Sprintf("Current weather in %s: %.1f deg%s, %s (source: %s).", payload.Location, payload.Temperature, unitSymbol, payload.Condition, payload.Source)
		}
	case "air_quality":
		var payload AirQualityResponse
		if json.Unmarshal([]byte(rawResult), &payload) == nil && payload.Location != "" {
			scale := strings.ToUpper(strings.TrimSpace(payload.Scale))
			if scale == "" {
				scale = "AQI"
			} else {
				scale += " AQI"
			}
			return fmt.Sprintf(
				"Current air quality in %s: %s %.0f (%s), PM2.5 %.1f ug/m3, PM10 %.1f ug/m3 (source: %s).",
				payload.Location,
				scale,
				payload.AQI,
				strings.TrimSpace(payload.Category),
				payload.PM25,
				payload.PM10,
				payload.Source,
			)
		}
	case "web_search":
		var payload WebSearchResponse
		if json.Unmarshal([]byte(rawResult), &payload) == nil {
			if len(payload.Results) == 0 {
				return fmt.Sprintf("No web results found for \"%s\".", payload.Query)
			}
			lines := []string{fmt.Sprintf("Top web results for \"%s\":", payload.Query)}
			limit := 3
			if len(payload.Results) < limit {
				limit = len(payload.Results)
			}
			for i := 0; i < limit; i++ {
				item := payload.Results[i]
				lines = append(lines, fmt.Sprintf("%d. %s - %s", i+1, strings.TrimSpace(item.Title), strings.TrimSpace(item.URL)))
			}
			return strings.Join(lines, "\n")
		}
	case "web_fetch":
		var payload WebFetchResponse
		if json.Unmarshal([]byte(rawResult), &payload) == nil {
			if payload.Summary != "" {
				if payload.Title != "" {
					return fmt.Sprintf("%s\n%s", payload.Title, payload.Summary)
				}
				return payload.Summary
			}
			if payload.Content != "" {
				return payload.Content
			}
		}
	case "browser":
		var payload BrowserResponse
		if json.Unmarshal([]byte(rawResult), &payload) == nil {
			if payload.Result != "" {
				return payload.Result
			}
		}
	}
	return strings.TrimSpace(rawResult)
}

func formatUtilityDirectError(toolName string, err error) string {
	if err == nil {
		return "Utility request failed."
	}
	errText := strings.TrimSpace(err.Error())
	if strings.Contains(strings.ToLower(errText), "invalid") || strings.Contains(strings.ToLower(errText), "required") {
		return fmt.Sprintf("I could not run %s because the input was invalid: %s.", toolName, errText)
	}
	if strings.Contains(strings.ToLower(errText), "timed out") {
		return fmt.Sprintf("I could not complete %s in time. Please try again.", toolName)
	}
	return fmt.Sprintf("I could not complete %s right now: %s.", toolName, errText)
}
