package pluginapi

import (
	"fmt"
	"sort"
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

	if len(y.Operations) == 0 {
		// Build JSON Schema for parameters
		properties, required, err := buildParametersSchema(y.Parameters)
		if err != nil {
			return Tool{}, err
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

	// Build union of all parameter schemas for tool hints
	allParams := make(map[string]YAMLToolParameter)
	if err := addParameterDefinitions(allParams, y.Parameters); err != nil {
		return Tool{}, err
	}

	operationNames := sortedOperationNames(y.Operations)
	for _, opName := range operationNames {
		opDef := y.Operations[opName]
		if err := addParameterDefinitions(allParams, opDef.Parameters); err != nil {
			return Tool{}, err
		}
	}

	// Auto-derive enum from operations keys if not explicitly provided
	if opParam, ok := allParams["operation"]; ok && len(opParam.Enum) == 0 {
		opParam.Enum = operationNames
		allParams["operation"] = opParam
	}

	properties := make(map[string]interface{}, len(allParams))
	for name, param := range allParams {
		paramSchema, err := buildParameterSchema(name, param)
		if err != nil {
			return Tool{}, fmt.Errorf("parameter %q: %w", name, err)
		}
		properties[name] = paramSchema
	}

	_, globalRequired, err := buildParametersSchema(y.Parameters)
	if err != nil {
		return Tool{}, err
	}

	_, ok := allParams["operation"]
	if !ok {
		return Tool{}, fmt.Errorf("operation parameter is required when operations are defined")
	}

	// Build a flat schema for LLM compatibility (OpenAI doesn't support oneOf at top level)
	// All parameters are included, and operation-specific validation happens server-side
	// via ValidateToolParametersWithOperations
	parametersSchema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}

	// Only operation is required at top level; operation-specific required params
	// are validated server-side
	required := globalRequired
	if !containsString(required, "operation") {
		required = append(required, "operation")
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

func buildParametersSchema(params []YAMLToolParameter) (map[string]interface{}, []string, error) {
	properties := make(map[string]interface{})
	var required []string

	for _, param := range params {
		if param.Name == "" {
			return nil, nil, fmt.Errorf("parameter name is required")
		}

		paramSchema, err := buildParameterSchema(param.Name, param)
		if err != nil {
			return nil, nil, fmt.Errorf("parameter %q: %w", param.Name, err)
		}

		properties[param.Name] = paramSchema
		if param.Required {
			required = append(required, param.Name)
		}
	}

	return properties, required, nil
}

func addParameterDefinitions(all map[string]YAMLToolParameter, params []YAMLToolParameter) error {
	for _, param := range params {
		if param.Name == "" {
			return fmt.Errorf("parameter name is required")
		}
		if existing, ok := all[param.Name]; ok {
			if existing.Type != param.Type {
				return fmt.Errorf("parameter %q has conflicting types: %s vs %s", param.Name, existing.Type, param.Type)
			}
			continue
		}
		all[param.Name] = param
	}
	return nil
}

func sortedOperationNames(operations map[string]YAMLOperationDefinition) []string {
	if len(operations) == 0 {
		return nil
	}

	names := make([]string, 0, len(operations))
	for name := range operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// ValidateToolParameters validates tool parameters against the JSON schema generated for the tool.
// For basic schemas, it validates required fields. For operation-based tools, use
// ValidateToolParametersWithOperations for full operation-specific validation.
func ValidateToolParameters(schema map[string]interface{}, params map[string]interface{}) error {
	if schema == nil {
		return nil
	}

	properties := extractProperties(schema)
	required := extractRequired(schema)
	return validateRequiredParams(required, properties, params)
}

// ValidateToolParametersWithOperations validates tool parameters using the YAML tool definition.
// This provides operation-specific validation where each operation can have its own required parameters.
func ValidateToolParametersWithOperations(toolDef *YAMLToolDefinition, params map[string]interface{}) error {
	if toolDef == nil {
		return nil
	}

	// If no operations defined, fall back to simple validation
	if len(toolDef.Operations) == 0 {
		// Check global required params
		for _, param := range toolDef.Parameters {
			if param.Required {
				if isMissingParam(param, params) {
					return fmt.Errorf("required field '%s' is missing", param.Name)
				}
			}
		}
		return nil
	}

	// Get operation value
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return fmt.Errorf("required field 'operation' is missing")
	}

	// Find operation definition
	opDef, ok := toolDef.Operations[operation]
	if !ok {
		// Check if operation is valid based on enum or operations keys
		operationParam, found := findParameter(toolDef.Parameters, "operation")
		if found && len(operationParam.Enum) > 0 {
			if !containsString(operationParam.Enum, operation) {
				return fmt.Errorf("unknown operation: %s", operation)
			}
		} else {
			return fmt.Errorf("unknown operation: %s", operation)
		}
	}

	// Validate global required params
	for _, param := range toolDef.Parameters {
		if param.Required && param.Name != "operation" {
			if isMissingParam(param, params) {
				return fmt.Errorf("required field '%s' is missing", param.Name)
			}
		}
	}

	// Validate operation-specific required params
	for _, param := range opDef.Parameters {
		if param.Required {
			if isMissingParam(param, params) {
				return fmt.Errorf("required field '%s' is missing", param.Name)
			}
		}
	}

	return nil
}

// isMissingParam checks if a required parameter is missing from the params map
func isMissingParam(param YAMLToolParameter, params map[string]interface{}) bool {
	value, exists := params[param.Name]
	if !exists {
		return true
	}
	if value == nil {
		return true
	}

	// Type-specific empty checks
	switch param.Type {
	case "string":
		if v, ok := value.(string); ok && v == "" {
			return true
		}
	}

	return false
}

func extractProperties(schema map[string]interface{}) map[string]interface{} {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return nil
	}
	return props
}

func extractRequired(schema map[string]interface{}) []string {
	requiredRaw, ok := schema["required"]
	if !ok {
		return nil
	}

	switch v := requiredRaw.(type) {
	case []string:
		return v
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}

func validateRequiredParams(required []string, properties map[string]interface{}, params map[string]interface{}) error {
	for _, name := range required {
		value, exists := params[name]
		if !exists {
			return fmt.Errorf("required field '%s' is missing", name)
		}
		if isMissingValue(name, value, properties) {
			return fmt.Errorf("required field '%s' is missing", name)
		}
	}
	return nil
}

func isMissingValue(name string, value interface{}, properties map[string]interface{}) bool {
	if value == nil {
		return true
	}

	prop := map[string]interface{}{}
	if properties != nil {
		if raw, ok := properties[name].(map[string]interface{}); ok {
			prop = raw
		}
	}

	paramType, _ := prop["type"].(string)
	switch paramType {
	case "string":
		if v, ok := value.(string); ok {
			return v == ""
		}
		return false
	case "integer", "number":
		switch v := value.(type) {
		case float64:
			return v == 0
		case float32:
			return v == 0
		case int:
			return v == 0
		case int64:
			return v == 0
		default:
			return false
		}
	default:
		return false
	}
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
	if len(toolDef.Parameters) == 0 && len(toolDef.Operations) == 0 {
		return fmt.Errorf("tool must have at least one parameter")
	}

	paramTypes := make(map[string]string)
	for _, param := range toolDef.Parameters {
		if param.Name == "" {
			return fmt.Errorf("parameter name is required")
		}
		if err := validateParameter(param.Name, param, ""); err != nil {
			return err
		}
		if existingType, ok := paramTypes[param.Name]; ok && existingType != param.Type {
			return fmt.Errorf("parameter %q has conflicting types: %s vs %s", param.Name, existingType, param.Type)
		}
		paramTypes[param.Name] = param.Type
	}

	if len(toolDef.Operations) > 0 {
		operationParam, ok := findParameter(toolDef.Parameters, "operation")
		if !ok {
			return fmt.Errorf("operation parameter is required when operations are defined")
		}
		if operationParam.Type != "string" {
			return fmt.Errorf("operation parameter must be type string")
		}
		if !operationParam.Required {
			return fmt.Errorf("operation parameter must be required when operations are defined")
		}

		// Validate operation names
		for opName := range toolDef.Operations {
			if opName == "" {
				return fmt.Errorf("operation name cannot be empty")
			}
		}

		// If enum is explicitly provided, validate it matches operations
		if len(operationParam.Enum) > 0 {
			for opName := range toolDef.Operations {
				if !containsString(operationParam.Enum, opName) {
					return fmt.Errorf("operation parameter enum missing value %q", opName)
				}
			}
		}
		// If enum is empty, it will be auto-derived from operations keys in ToToolDefinition

		for _, opDef := range toolDef.Operations {
			for _, param := range opDef.Parameters {
				if param.Name == "" {
					return fmt.Errorf("parameter name is required")
				}
				if err := validateParameter(param.Name, param, ""); err != nil {
					return err
				}
				if existingType, ok := paramTypes[param.Name]; ok && existingType != param.Type {
					return fmt.Errorf("parameter %q has conflicting types: %s vs %s", param.Name, existingType, param.Type)
				}
				paramTypes[param.Name] = param.Type
			}
		}
	}

	return nil
}

func findParameter(params []YAMLToolParameter, name string) (YAMLToolParameter, bool) {
	for _, param := range params {
		if param.Name == name {
			return param, true
		}
	}
	return YAMLToolParameter{}, false
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

// GetOperationsFromYAML extracts operation information from a YAMLToolDefinition.
// This helper makes it easy for plugins to implement the OperationsProvider interface.
func GetOperationsFromYAML(toolDef *YAMLToolDefinition) []OperationInfo {
	if toolDef == nil || len(toolDef.Operations) == 0 {
		return nil
	}

	operationNames := sortedOperationNames(toolDef.Operations)
	operations := make([]OperationInfo, 0, len(operationNames))

	for _, opName := range operationNames {
		opDef := toolDef.Operations[opName]

		var params []string
		var requiredParams []string

		for _, param := range opDef.Parameters {
			params = append(params, param.Name)
			if param.Required {
				requiredParams = append(requiredParams, param.Name)
			}
		}

		sort.Strings(params)
		sort.Strings(requiredParams)

		operations = append(operations, OperationInfo{
			Name:               opName,
			Parameters:         params,
			RequiredParameters: requiredParams,
		})
	}

	return operations
}
