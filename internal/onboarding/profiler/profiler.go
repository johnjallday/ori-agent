// Package profiler provides AI-powered user profile inference
// based on detected applications or user self-description.
package profiler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/onboarding/detector"
)

// ProfileCategory represents a user profile category.
type ProfileCategory string

const (
	CategoryDeveloper      ProfileCategory = "developer"
	CategoryDevOps         ProfileCategory = "devops"
	CategoryDesigner       ProfileCategory = "designer"
	CategoryDataScientist  ProfileCategory = "data_scientist"
	CategoryWriter         ProfileCategory = "writer"
	CategoryProjectManager ProfileCategory = "project_manager"
	CategoryGeneral        ProfileCategory = "general"
)

// UserProfile represents the inferred user profile.
type UserProfile struct {
	// PrimaryCategory is the main profile category.
	PrimaryCategory ProfileCategory `json:"primary_category"`

	// SecondaryCategories are additional relevant categories.
	SecondaryCategories []ProfileCategory `json:"secondary_categories,omitempty"`

	// Specializations are specific areas within the categories.
	// e.g., "Go developer", "iOS developer", "UI/UX designer"
	Specializations []string `json:"specializations,omitempty"`

	// Summary is a natural language description of the profile.
	Summary string `json:"summary"`

	// Confidence is the AI's confidence in the profile (0-1).
	Confidence float64 `json:"confidence"`

	// DetectedApps are the apps that influenced this profile.
	DetectedApps []string `json:"detected_apps,omitempty"`

	// SuggestedAgentNames are recommended agent names for this profile.
	SuggestedAgentNames []string `json:"suggested_agent_names,omitempty"`

	// SuggestedPlugins are recommended plugins for this profile.
	SuggestedPlugins []string `json:"suggested_plugins,omitempty"`
}

// Profiler analyzes apps or descriptions to infer user profiles.
type Profiler struct {
	llmProvider llm.Provider
	model       string
}

// New creates a new Profiler with the given LLM provider and model.
func New(provider llm.Provider, model string) *Profiler {
	if model == "" {
		model = "gpt-4"
	}
	return &Profiler{
		llmProvider: provider,
		model:       model,
	}
}

// InferFromApps analyzes detected applications to infer a user profile.
func (p *Profiler) InferFromApps(ctx context.Context, apps []detector.DetectedApp) (*UserProfile, error) {
	if len(apps) == 0 {
		return &UserProfile{
			PrimaryCategory: CategoryGeneral,
			Summary:         "No applications detected. Please describe yourself to get personalized recommendations.",
			Confidence:      0,
		}, nil
	}

	// Build app list for the prompt
	var appNames []string
	for _, app := range apps {
		appNames = append(appNames, app.Name)
	}

	prompt := buildAppsPrompt(appNames)
	return p.inferProfile(ctx, prompt, appNames)
}

// InferFromDescription analyzes user's self-description to infer a profile.
func (p *Profiler) InferFromDescription(ctx context.Context, description string) (*UserProfile, error) {
	if strings.TrimSpace(description) == "" {
		return &UserProfile{
			PrimaryCategory: CategoryGeneral,
			Summary:         "No description provided.",
			Confidence:      0,
		}, nil
	}

	prompt := buildDescriptionPrompt(description)
	return p.inferProfile(ctx, prompt, nil)
}

// inferProfile sends the prompt to the LLM and parses the response.
func (p *Profiler) inferProfile(ctx context.Context, prompt string, detectedApps []string) (*UserProfile, error) {
	req := llm.ChatRequest{
		Model: p.model,
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		Temperature: 0.3, // Lower temperature for more consistent JSON output
	}

	resp, err := p.llmProvider.Chat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get LLM response: %w", err)
	}

	profile, err := parseProfileResponse(resp.Content)
	if err != nil {
		// Fallback to basic profile if parsing fails
		return &UserProfile{
			PrimaryCategory: CategoryGeneral,
			Summary:         "Unable to determine profile. Using default configuration.",
			Confidence:      0.3,
			DetectedApps:    detectedApps,
		}, nil
	}

	profile.DetectedApps = detectedApps
	return profile, nil
}

// buildAppsPrompt creates the prompt for app-based inference.
func buildAppsPrompt(appNames []string) string {
	return fmt.Sprintf(`Analyze the following list of recently used applications and infer the user's profile.

Recently Used Applications:
%s

Based on these applications, determine:
1. The user's primary role/profession
2. Any secondary roles
3. Specific specializations (e.g., "Go developer", "iOS developer", "UI/UX designer")
4. A brief natural language summary (1-2 sentences)
5. Confidence level (0-1)
6. Suggested AI agent names that would help this user (2-3 names)
7. Suggested plugins that would be useful (2-4 plugin types)

Respond in JSON format:
{
  "primary_category": "developer|devops|designer|data_scientist|writer|project_manager|general",
  "secondary_categories": ["category1", "category2"],
  "specializations": ["specific area 1", "specific area 2"],
  "summary": "Brief description of the user's profile",
  "confidence": 0.85,
  "suggested_agent_names": ["Code Assistant", "Git Helper"],
  "suggested_plugins": ["shell-executor", "git-tools"]
}

Only respond with the JSON object, no additional text.`, strings.Join(appNames, "\n"))
}

// buildDescriptionPrompt creates the prompt for description-based inference.
func buildDescriptionPrompt(description string) string {
	return fmt.Sprintf(`Analyze the following user self-description and infer their profile.

User Description:
"%s"

Based on this description, determine:
1. The user's primary role/profession
2. Any secondary roles
3. Specific specializations
4. A brief natural language summary (1-2 sentences)
5. Confidence level (0-1)
6. Suggested AI agent names that would help this user (2-3 names)
7. Suggested plugins that would be useful (2-4 plugin types)

Respond in JSON format:
{
  "primary_category": "developer|devops|designer|data_scientist|writer|project_manager|general",
  "secondary_categories": ["category1", "category2"],
  "specializations": ["specific area 1", "specific area 2"],
  "summary": "Brief description of the user's profile",
  "confidence": 0.85,
  "suggested_agent_names": ["Code Assistant", "Git Helper"],
  "suggested_plugins": ["shell-executor", "git-tools"]
}

Only respond with the JSON object, no additional text.`, description)
}

// fixJSONNewlines fixes unescaped newlines in JSON string values
// LLMs sometimes output actual newlines inside JSON strings which is invalid
func fixJSONNewlines(s string) string {
	var result strings.Builder
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if escaped {
			result.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && inString {
			escaped = true
			result.WriteByte(c)
			continue
		}

		if c == '"' {
			inString = !inString
			result.WriteByte(c)
			continue
		}

		// If we're inside a string and see an actual newline, replace with space
		if inString && (c == '\n' || c == '\r') {
			result.WriteByte(' ')
			continue
		}

		result.WriteByte(c)
	}

	return result.String()
}

// rawProfileResponse is a flexible struct for parsing LLM responses
// that may have varying formats for suggested_plugins
type rawProfileResponse struct {
	PrimaryCategory     string          `json:"primary_category"`
	SecondaryCategories []string        `json:"secondary_categories"`
	Specializations     []string        `json:"specializations"`
	Summary             string          `json:"summary"`
	Confidence          float64         `json:"confidence"`
	SuggestedAgentNames []string        `json:"suggested_agent_names"`
	SuggestedPlugins    json.RawMessage `json:"suggested_plugins"` // Can be []string or []object
}

// parseProfileResponse parses the LLM's JSON response into a UserProfile.
func parseProfileResponse(response string) (*UserProfile, error) {
	// Clean up the response - remove markdown code blocks if present
	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	// Fix common JSON issues from LLMs:
	// 1. Replace actual newlines inside string values with escaped newlines
	// We need to be careful to only do this inside string values
	response = fixJSONNewlines(response)

	// First try to parse into the raw struct
	var raw rawProfileResponse
	if err := json.Unmarshal([]byte(response), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse profile JSON: %w (response: %.200s...)", err, response)
	}

	// Convert to UserProfile
	profile := &UserProfile{
		PrimaryCategory:     ProfileCategory(raw.PrimaryCategory),
		Specializations:     raw.Specializations,
		Summary:             raw.Summary,
		Confidence:          raw.Confidence,
		SuggestedAgentNames: raw.SuggestedAgentNames,
	}

	// Convert secondary categories
	for _, cat := range raw.SecondaryCategories {
		profile.SecondaryCategories = append(profile.SecondaryCategories, ProfileCategory(cat))
	}

	// Parse suggested_plugins flexibly - it could be []string or []object
	if len(raw.SuggestedPlugins) > 0 {
		// Try parsing as []string first
		var pluginStrings []string
		if err := json.Unmarshal(raw.SuggestedPlugins, &pluginStrings); err == nil {
			profile.SuggestedPlugins = pluginStrings
		} else {
			// Try parsing as []object with "type" or "name" field
			var pluginObjects []map[string]any
			if err := json.Unmarshal(raw.SuggestedPlugins, &pluginObjects); err == nil {
				for _, obj := range pluginObjects {
					// Try "type" first, then "name"
					if t, ok := obj["type"].(string); ok && t != "" {
						profile.SuggestedPlugins = append(profile.SuggestedPlugins, t)
					} else if n, ok := obj["name"].(string); ok && n != "" {
						profile.SuggestedPlugins = append(profile.SuggestedPlugins, n)
					}
				}
			}
		}
	}

	// Validate and normalize
	if profile.PrimaryCategory == "" {
		profile.PrimaryCategory = CategoryGeneral
	}
	if profile.Confidence < 0 {
		profile.Confidence = 0
	}
	if profile.Confidence > 1 {
		profile.Confidence = 1
	}

	return profile, nil
}

// GetCategoryDisplayName returns a human-readable name for a category.
func GetCategoryDisplayName(cat ProfileCategory) string {
	names := map[ProfileCategory]string{
		CategoryDeveloper:      "Software Developer",
		CategoryDevOps:         "DevOps Engineer",
		CategoryDesigner:       "Designer",
		CategoryDataScientist:  "Data Scientist",
		CategoryWriter:         "Writer / Content Creator",
		CategoryProjectManager: "Project Manager",
		CategoryGeneral:        "General User",
	}
	if name, ok := names[cat]; ok {
		return name
	}
	return string(cat)
}
