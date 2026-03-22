package toolapi

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// DisplayType defines how the result should be displayed in the UI.
type DisplayType string

const (
	DisplayTypeText  DisplayType = "text"
	DisplayTypeTable DisplayType = "table"
	DisplayTypeModal DisplayType = "modal"
	DisplayTypeCard  DisplayType = "card"
	DisplayTypeList  DisplayType = "list"
	DisplayTypeJSON  DisplayType = "json"
)

// StructuredResult represents a tool result with metadata about how to display it.
type StructuredResult struct {
	DisplayType DisplayType    `json:"displayType" yaml:"displayType"`
	Title       string         `json:"title,omitempty" yaml:"title,omitempty"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Data        interface{}    `json:"data" yaml:"data"`
	Metadata    map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// ParseStructuredResult attempts to parse a result string as either JSON or YAML.
func ParseStructuredResult(result string) (*StructuredResult, error) {
	var sr StructuredResult
	if err := json.Unmarshal([]byte(result), &sr); err == nil {
		return &sr, nil
	}
	if err := yaml.Unmarshal([]byte(result), &sr); err == nil {
		return &sr, nil
	}
	return nil, fmt.Errorf("result is not a valid structured result (neither JSON nor YAML)")
}
