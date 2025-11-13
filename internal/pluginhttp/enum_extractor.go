package pluginhttp

import (
	"fmt"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// EnumExtractor provides functionality to extract enum values from OpenAI function parameters
type EnumExtractor struct{}

// NewEnumExtractor creates a new EnumExtractor instance
func NewEnumExtractor() *EnumExtractor {
	return &EnumExtractor{}
}

// ExtractEnumsFromParameter extracts all enum values from a function parameter
func (e *EnumExtractor) ExtractEnumsFromParameter(param pluginapi.Tool, propertyName string) ([]string, error) {
	// Get the properties from the parameters
	properties, ok := param.Parameters["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no properties found in function parameters")
	}

	// Get the specific property
	property, ok := properties[propertyName].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("property %s not found", propertyName)
	}

	// Extract enum values if they exist
	enumInterface, ok := property["enum"]
	if !ok {
		return nil, fmt.Errorf("no enum found for property %s", propertyName)
	}

	// Convert interface{} to []string
	var enumValues []string
	switch enum := enumInterface.(type) {
	case []string:
		enumValues = enum
	case []interface{}:
		for _, val := range enum {
			if strVal, ok := val.(string); ok {
				enumValues = append(enumValues, strVal)
			} else {
				return nil, fmt.Errorf("enum value is not a string: %v", val)
			}
		}
	default:
		return nil, fmt.Errorf("enum is not a slice of strings or interfaces: %T", enum)
	}

	return enumValues, nil
}

// GetAllEnumsFromParameter returns a map of property names to their enum values
func (e *EnumExtractor) GetAllEnumsFromParameter(param pluginapi.Tool) (map[string][]string, error) {
	result := make(map[string][]string)

	// Get the properties from the parameters
	properties, ok := param.Parameters["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no properties found in function parameters")
	}

	// Iterate through all properties to find enums
	for propName, propValue := range properties {
		property, ok := propValue.(map[string]any)
		if !ok {
			continue
		}

		// Check if this property has an enum
		enumInterface, exists := property["enum"]
		if !exists {
			continue
		}

		// Extract enum values
		var enumValues []string
		switch enum := enumInterface.(type) {
		case []string:
			enumValues = enum
		case []interface{}:
			for _, val := range enum {
				if strVal, ok := val.(string); ok {
					enumValues = append(enumValues, strVal)
				}
			}
		}

		if len(enumValues) > 0 {
			result[propName] = enumValues
		}
	}

	return result, nil
}

// ValidateEnumValue checks if a given value is valid for a specific property's enum
func (e *EnumExtractor) ValidateEnumValue(param pluginapi.Tool, propertyName, value string) (bool, error) {
	enumValues, err := e.ExtractEnumsFromParameter(param, propertyName)
	if err != nil {
		return false, err
	}

	for _, enumValue := range enumValues {
		if enumValue == value {
			return true, nil
		}
	}

	return false, nil
}
