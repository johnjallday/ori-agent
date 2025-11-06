package pluginapi

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// DisplayType defines how the result should be displayed in the UI
type DisplayType string

const (
	DisplayTypeText  DisplayType = "text"  // Plain text response
	DisplayTypeTable DisplayType = "table" // Tabular data
	DisplayTypeModal DisplayType = "modal" // Modal/popup with interactive elements
	DisplayTypeCard  DisplayType = "card"  // Card-based layout
	DisplayTypeList  DisplayType = "list"  // Simple list
	DisplayTypeJSON  DisplayType = "json"  // Raw JSON viewer
)

// StructuredResult represents a plugin result with metadata about how to display it
type StructuredResult struct {
	DisplayType DisplayType    `json:"displayType" yaml:"displayType"`
	Title       string         `json:"title,omitempty" yaml:"title,omitempty"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Data        interface{}    `json:"data" yaml:"data"`
	Metadata    map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// ToJSON converts the StructuredResult to a JSON string
func (sr *StructuredResult) ToJSON() (string, error) {
	data, err := json.Marshal(sr)
	if err != nil {
		return "", fmt.Errorf("failed to marshal structured result: %w", err)
	}
	return string(data), nil
}

// ToYAML converts the StructuredResult to a YAML string
func (sr *StructuredResult) ToYAML() (string, error) {
	data, err := yaml.Marshal(sr)
	if err != nil {
		return "", fmt.Errorf("failed to marshal structured result: %w", err)
	}
	return string(data), nil
}

// FromJSON parses a JSON string into a StructuredResult
func FromJSON(jsonStr string) (*StructuredResult, error) {
	var sr StructuredResult
	if err := json.Unmarshal([]byte(jsonStr), &sr); err != nil {
		return nil, fmt.Errorf("failed to unmarshal structured result: %w", err)
	}
	return &sr, nil
}

// FromYAML parses a YAML string into a StructuredResult
func FromYAML(yamlStr string) (*StructuredResult, error) {
	var sr StructuredResult
	if err := yaml.Unmarshal([]byte(yamlStr), &sr); err != nil {
		return nil, fmt.Errorf("failed to unmarshal structured result: %w", err)
	}
	return &sr, nil
}

// IsStructuredResult checks if a string is a valid structured result (JSON or YAML)
func IsStructuredResult(result string) bool {
	// Try JSON first
	if _, err := FromJSON(result); err == nil {
		return true
	}
	// Try YAML
	if _, err := FromYAML(result); err == nil {
		return true
	}
	return false
}

// ParseStructuredResult attempts to parse a result string as either JSON or YAML
func ParseStructuredResult(result string) (*StructuredResult, error) {
	// Try JSON first (more common)
	if sr, err := FromJSON(result); err == nil {
		return sr, nil
	}
	// Try YAML
	if sr, err := FromYAML(result); err == nil {
		return sr, nil
	}
	return nil, fmt.Errorf("result is not a valid structured result (neither JSON nor YAML)")
}

// NewTableResult creates a StructuredResult for tabular data
func NewTableResult(title string, columns []string, data interface{}) *StructuredResult {
	return &StructuredResult{
		DisplayType: DisplayTypeTable,
		Title:       title,
		Data:        data,
		Metadata: map[string]any{
			"columns": columns,
		},
	}
}

// NewModalResult creates a StructuredResult for modal display
func NewModalResult(title, description string, data interface{}) *StructuredResult {
	return &StructuredResult{
		DisplayType: DisplayTypeModal,
		Title:       title,
		Description: description,
		Data:        data,
		Metadata:    make(map[string]any),
	}
}

// NewTextResult creates a StructuredResult for plain text
func NewTextResult(text string) *StructuredResult {
	return &StructuredResult{
		DisplayType: DisplayTypeText,
		Data:        text,
	}
}

// NewListResult creates a StructuredResult for list display
func NewListResult(title string, items interface{}) *StructuredResult {
	return &StructuredResult{
		DisplayType: DisplayTypeList,
		Title:       title,
		Data:        items,
	}
}
