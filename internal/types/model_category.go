package types

// ModelCategory represents a user-defined or default category for organizing models
type ModelCategory struct {
	ID        string `json:"id"`                   // Unique identifier (e.g., "cat_abc123")
	Name      string `json:"name"`                 // Display name
	Color     string `json:"color"`                // Hex color code (e.g., "#3b82f6")
	Icon      string `json:"icon"`                 // Bootstrap icon name (e.g., "code-slash")
	Order     int    `json:"order"`                // Sort order (0-based)
	IsDefault bool   `json:"is_default,omitempty"` // True for built-in categories
	IsHidden  bool   `json:"is_hidden,omitempty"`  // True if user has hidden this category
}

// ModelCategoryConfig holds all category configuration including assignments
type ModelCategoryConfig struct {
	Categories       []ModelCategory     `json:"categories"`
	ModelAssignments map[string][]string `json:"model_assignments"` // model ID -> category IDs
	ViewPreference   string              `json:"view_preference"`   // "provider" or "category"
}

// DefaultCategories returns the built-in categories
func DefaultCategories() []ModelCategory {
	return []ModelCategory{
		{
			ID:        "cat_default_tool_calling",
			Name:      "Tool Calling",
			Color:     "#7c3aed",
			Icon:      "tools",
			Order:     0,
			IsDefault: true,
		},
		{
			ID:        "cat_default_general_purpose",
			Name:      "General Purpose",
			Color:     "#3b82f6",
			Icon:      "chat-dots",
			Order:     1,
			IsDefault: true,
		},
		{
			ID:        "cat_default_research",
			Name:      "Research",
			Color:     "#16a34a",
			Icon:      "search",
			Order:     2,
			IsDefault: true,
		},
	}
}

// NewModelCategoryConfig creates a new config with default categories and assignments
func NewModelCategoryConfig() *ModelCategoryConfig {
	return &ModelCategoryConfig{
		Categories:       DefaultCategories(),
		ModelAssignments: DefaultModelAssignments(),
		ViewPreference:   "provider",
	}
}

// DefaultModelAssignments returns pre-configured category assignments for common models
func DefaultModelAssignments() map[string][]string {
	toolCalling := "cat_default_tool_calling"
	generalPurpose := "cat_default_general_purpose"
	research := "cat_default_research"

	return map[string][]string{
		// OpenAI Models
		"gpt-5":        {generalPurpose, research},
		"gpt-5-mini":   {toolCalling, generalPurpose},
		"gpt-5-nano":   {toolCalling},
		"gpt-4.1":      {generalPurpose, research},
		"gpt-4.1-mini": {toolCalling, generalPurpose},
		"gpt-4.1-nano": {toolCalling},

		// Anthropic Claude Models
		"claude-sonnet-4-5":          {generalPurpose, research},
		"claude-sonnet-4":            {generalPurpose},
		"claude-opus-4-1":            {research},
		"claude-3-5-sonnet-latest":   {toolCalling, generalPurpose},
		"claude-3-5-sonnet-20241022": {toolCalling, generalPurpose},
		"claude-3-opus-20240229":     {research},
		"claude-3-sonnet-20240229":   {generalPurpose},
		"claude-3-haiku-20240307":    {toolCalling},

		// Ollama / Local Models
		"llama3":         {toolCalling, generalPurpose},
		"llama3:8b":      {toolCalling},
		"llama3:70b":     {generalPurpose, research},
		"llama2":         {generalPurpose},
		"mistral":        {toolCalling},
		"mixtral":        {generalPurpose, research},
		"codellama":      {toolCalling},
		"phi":            {toolCalling},
		"qwen":           {toolCalling},
		"deepseek-coder": {toolCalling},
	}
}

// MaxCustomCategories is the maximum number of custom categories allowed
const MaxCustomCategories = 20

// PredefinedColors are the default color options for categories
var PredefinedColors = []string{
	"#ef4444", // Red
	"#f97316", // Orange
	"#eab308", // Yellow
	"#22c55e", // Green
	"#14b8a6", // Teal
	"#3b82f6", // Blue
	"#8b5cf6", // Purple
	"#ec4899", // Pink
}

// PredefinedIcons are the default icon options for categories
var PredefinedIcons = []string{
	"code-slash",
	"tools",
	"chat-dots",
	"search",
	"lightbulb",
	"gear",
	"file-text",
	"graph-up",
	"pencil",
	"book",
	"cpu",
	"globe",
	"shield-check",
	"terminal",
	"robot",
	"magic",
}
