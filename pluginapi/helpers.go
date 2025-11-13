package pluginapi

// Helper functions for building JSON schema parameter definitions.
// These functions make it easier to construct tool definitions without
// manually building complex map[string]interface{} structures.

// NewTool creates a new Tool with the given name, description, and parameters.
//
// Example:
//
//	tool := pluginapi.NewTool(
//	    "calculate",
//	    "Perform a mathematical calculation",
//	    pluginapi.ObjectProperty("", map[string]interface{}{
//	        "expression": pluginapi.StringProperty("Mathematical expression to evaluate"),
//	    }, []string{"expression"}),
//	)
func NewTool(name, description string, parameters map[string]interface{}) Tool {
	return Tool{
		Name:        name,
		Description: description,
		Parameters:  parameters,
	}
}

// StringProperty creates a JSON schema property for a string parameter.
//
// Example:
//
//	"location": pluginapi.StringProperty("The city and state, e.g. San Francisco, CA")
func StringProperty(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"description": description,
	}
}

// NumberProperty creates a JSON schema property for a number parameter.
//
// Example:
//
//	"temperature": pluginapi.NumberProperty("Temperature in degrees")
func NumberProperty(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "number",
		"description": description,
	}
}

// IntegerProperty creates a JSON schema property for an integer parameter.
//
// Example:
//
//	"count": pluginapi.IntegerProperty("Number of items")
func IntegerProperty(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "integer",
		"description": description,
	}
}

// BooleanProperty creates a JSON schema property for a boolean parameter.
//
// Example:
//
//	"verbose": pluginapi.BooleanProperty("Enable verbose output")
func BooleanProperty(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "boolean",
		"description": description,
	}
}

// ArrayProperty creates a JSON schema property for an array parameter.
// The items parameter defines the schema for array elements.
//
// Example:
//
//	"tags": pluginapi.ArrayProperty("List of tags", pluginapi.StringProperty("Tag name"))
func ArrayProperty(description string, items map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": description,
		"items":       items,
	}
}

// ObjectProperty creates a JSON schema property for an object parameter.
// The properties parameter defines the object's fields, and required lists
// the required field names.
//
// Example:
//
//	parameters := pluginapi.ObjectProperty("Request parameters", map[string]interface{}{
//	    "name": pluginapi.StringProperty("User's name"),
//	    "age":  pluginapi.IntegerProperty("User's age"),
//	}, []string{"name"})
func ObjectProperty(description string, properties map[string]interface{}, required []string) map[string]interface{} {
	obj := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}

	if description != "" {
		obj["description"] = description
	}

	if len(required) > 0 {
		obj["required"] = required
	}

	return obj
}

// EnumProperty creates a JSON schema property with enumerated values.
// The type parameter should be "string", "number", or "integer".
//
// Example:
//
//	"unit": pluginapi.EnumProperty("string", "Temperature unit", []interface{}{"celsius", "fahrenheit"})
func EnumProperty(typ, description string, values []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":        typ,
		"description": description,
		"enum":        values,
	}
}

// StringEnumProperty is a convenience function for string enums.
//
// Example:
//
//	"format": pluginapi.StringEnumProperty("Output format", []string{"json", "yaml", "xml"})
func StringEnumProperty(description string, values []string) map[string]interface{} {
	// Convert []string to []interface{}
	interfaceValues := make([]interface{}, len(values))
	for i, v := range values {
		interfaceValues[i] = v
	}

	return EnumProperty("string", description, interfaceValues)
}

// WithMinMax adds minimum and maximum constraints to a number/integer property.
//
// Example:
//
//	"age": pluginapi.WithMinMax(pluginapi.IntegerProperty("User age"), 0, 150)
func WithMinMax(property map[string]interface{}, min, max float64) map[string]interface{} {
	property["minimum"] = min
	property["maximum"] = max
	return property
}

// WithDefault adds a default value to a property.
//
// Example:
//
//	"verbose": pluginapi.WithDefault(pluginapi.BooleanProperty("Enable verbose mode"), false)
func WithDefault(property map[string]interface{}, defaultValue interface{}) map[string]interface{} {
	property["default"] = defaultValue
	return property
}

// WithPattern adds a regex pattern constraint to a string property.
//
// Example:
//
//	"email": pluginapi.WithPattern(pluginapi.StringProperty("Email address"), "^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$")
func WithPattern(property map[string]interface{}, pattern string) map[string]interface{} {
	property["pattern"] = pattern
	return property
}
