package pluginapi

import (
	"fmt"
)

// ToToolDefinition converts a YAML tool definition to a pluginapi.Tool.
// This enables plugins to define their tool interface in plugin.yaml instead of code.
//
// Example plugin.yaml:
//
//	tool:
//	  name: weather
//	  description: Get weather information
//	  parameters:
//	    location:
//	      type: string
//	      description: City name or zip code
//	      required: true
//	    units:
//	      type: enum
//	      description: Temperature units
//	      enum: [celsius, fahrenheit]
//	      default: celsius
func (y *YAMLToolDefinition) ToToolDefinition() (Tool, error) {
	if y == nil {
		return Tool{}, fmt.Errorf("tool definition is nil")
	}

	// Validate required fields
	if y.Name == "" {
		return Tool{}, fmt.Errorf("tool name is required")
	}
	if y.Description == "" {
		return Tool{}, fmt.Errorf("tool description is required")
	}

	// Build JSON Schema for parameters
	properties := make(map[string]interface{})
	var required []string

	for _, param := range y.Parameters {
		// Get parameter name
		paramName := param.Name
		if paramName == "" {
			return Tool{}, fmt.Errorf("parameter name is required")
		}

		// Build parameter schema
		paramSchema, err := buildParameterSchema(paramName, param)
		if err != nil {
			return Tool{}, fmt.Errorf("parameter %q: %w", paramName, err)
		}

		properties[paramName] = paramSchema

		// Track required parameters
		if param.Required {
			required = append(required, paramName)
		}
	}

	// Build final parameters schema
	parametersSchema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}

	if len(required) > 0 {
		parametersSchema["required"] = required
	}

	return Tool{
		Name:        y.Name,
		Description: y.Description,
		Parameters:  parametersSchema,
	}, nil
}

// buildParameterSchema converts a YAMLToolParameter to JSON Schema format.
func buildParameterSchema(name string, param YAMLToolParameter) (map[string]interface{}, error) {
	schema := make(map[string]interface{})

	// Validate and set type
	if param.Type == "" {
		return nil, fmt.Errorf("type is required")
	}

	// Handle different parameter types
	switch param.Type {
	case "string":
		schema["type"] = "string"
		if param.Description != "" {
			schema["description"] = param.Description
		}
		if param.Default != nil {
			schema["default"] = param.Default
		}
		if len(param.Enum) > 0 {
			schema["enum"] = param.Enum
		}
		if param.MinLength != nil {
			schema["minLength"] = *param.MinLength
		}
		if param.MaxLength != nil {
			schema["maxLength"] = *param.MaxLength
		}
		if param.Pattern != "" {
			schema["pattern"] = param.Pattern
		}

	case "integer":
		schema["type"] = "integer"
		if param.Description != "" {
			schema["description"] = param.Description
		}
		if param.Default != nil {
			schema["default"] = param.Default
		}
		if param.Min != nil {
			schema["minimum"] = int(*param.Min)
		}
		if param.Max != nil {
			schema["maximum"] = int(*param.Max)
		}

	case "number":
		schema["type"] = "number"
		if param.Description != "" {
			schema["description"] = param.Description
		}
		if param.Default != nil {
			schema["default"] = param.Default
		}
		if param.Min != nil {
			schema["minimum"] = *param.Min
		}
		if param.Max != nil {
			schema["maximum"] = *param.Max
		}

	case "boolean":
		schema["type"] = "boolean"
		if param.Description != "" {
			schema["description"] = param.Description
		}
		if param.Default != nil {
			schema["default"] = param.Default
		}

	case "enum":
		if len(param.Enum) == 0 {
			return nil, fmt.Errorf("enum type requires 'enum' field with values")
		}
		schema["type"] = "string"
		if param.Description != "" {
			schema["description"] = param.Description
		}
		schema["enum"] = param.Enum
		if param.Default != nil {
			schema["default"] = param.Default
		}

	case "array":
		if param.Items == nil || param.Items.Type == "" {
			return nil, fmt.Errorf("array type requires 'items' field with type")
		}
		schema["type"] = "array"
		if param.Description != "" {
			schema["description"] = param.Description
		}
		schema["items"] = map[string]interface{}{
			"type": param.Items.Type,
		}
		if param.Default != nil {
			schema["default"] = param.Default
		}

	case "object":
		schema["type"] = "object"
		if param.Description != "" {
			schema["description"] = param.Description
		}

		// Recursively build nested properties
		if len(param.Properties) > 0 {
			nestedProps := make(map[string]interface{})
			var nestedRequired []string

			for propName, propParam := range param.Properties {
				propSchema, err := buildParameterSchema(propName, propParam)
				if err != nil {
					return nil, fmt.Errorf("nested property %q: %w", propName, err)
				}
				nestedProps[propName] = propSchema

				if propParam.Required {
					nestedRequired = append(nestedRequired, propName)
				}
			}

			schema["properties"] = nestedProps
			if len(nestedRequired) > 0 {
				schema["required"] = nestedRequired
			}
		}

		if param.Default != nil {
			schema["default"] = param.Default
		}

	default:
		return nil, fmt.Errorf("unsupported type: %s (supported: string, integer, number, boolean, enum, array, object)", param.Type)
	}

	return schema, nil
}

// ValidateYAMLToolDefinition performs comprehensive validation on a YAML tool definition.
// Returns detailed error messages to help plugin developers fix issues.
func ValidateYAMLToolDefinition(toolDef *YAMLToolDefinition) error {
	if toolDef == nil {
		return fmt.Errorf("tool definition cannot be nil")
	}

	// Validate name
	if toolDef.Name == "" {
		return fmt.Errorf("tool.name is required")
	}
	if len(toolDef.Name) > 64 {
		return fmt.Errorf("tool.name must be 64 characters or less (got %d)", len(toolDef.Name))
	}

	// Validate description
	if toolDef.Description == "" {
		return fmt.Errorf("tool.description is required")
	}
	if len(toolDef.Description) > 1024 {
		return fmt.Errorf("tool.description must be 1024 characters or less (got %d)", len(toolDef.Description))
	}

	// Validate parameters
	if len(toolDef.Parameters) == 0 {
		return fmt.Errorf("tool must have at least one parameter")
	}

	// Validate each parameter
	for _, param := range toolDef.Parameters {
		if param.Name == "" {
			return fmt.Errorf("parameter name is required")
		}
		if err := validateParameter(param.Name, param, ""); err != nil {
			return err
		}
	}

	return nil
}

// validateParameter validates a single parameter and its nested properties.
func validateParameter(name string, param YAMLToolParameter, prefix string) error {
	fullName := name
	if prefix != "" {
		fullName = prefix + "." + name
	}

	// Validate type
	validTypes := map[string]bool{
		"string": true, "integer": true, "number": true,
		"boolean": true, "enum": true, "array": true, "object": true,
	}
	if !validTypes[param.Type] {
		return fmt.Errorf("parameter %q: invalid type %q (must be one of: string, integer, number, boolean, enum, array, object)", fullName, param.Type)
	}

	// Validate description
	if param.Description == "" {
		return fmt.Errorf("parameter %q: description is required", fullName)
	}

	// Type-specific validation
	switch param.Type {
	case "enum":
		if len(param.Enum) == 0 {
			return fmt.Errorf("parameter %q: enum type requires 'enum' field with values", fullName)
		}
		// Validate default is in enum values
		if param.Default != nil {
			defaultStr, ok := param.Default.(string)
			if !ok {
				return fmt.Errorf("parameter %q: enum default must be a string", fullName)
			}
			found := false
			for _, v := range param.Enum {
				if v == defaultStr {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("parameter %q: default value %q is not in enum values", fullName, defaultStr)
			}
		}

	case "array":
		if param.Items == nil || param.Items.Type == "" {
			return fmt.Errorf("parameter %q: array type requires 'items' field with type", fullName)
		}

	case "object":
		if len(param.Properties) > 0 {
			for propName, propParam := range param.Properties {
				if err := validateParameter(propName, propParam, fullName); err != nil {
					return err
				}
			}
		}

	case "integer", "number":
		// Validate min/max
		if param.Min != nil && param.Max != nil {
			if *param.Min > *param.Max {
				return fmt.Errorf("parameter %q: min (%v) cannot be greater than max (%v)", fullName, *param.Min, *param.Max)
			}
		}

	case "string":
		// Validate min_length/max_length
		if param.MinLength != nil && param.MaxLength != nil {
			if *param.MinLength > *param.MaxLength {
				return fmt.Errorf("parameter %q: min_length (%d) cannot be greater than max_length (%d)", fullName, *param.MinLength, *param.MaxLength)
			}
		}
	}

	return nil
}
