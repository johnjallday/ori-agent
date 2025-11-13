package pluginapi

// Tool represents a generic, provider-agnostic function/tool definition.
// This structure is designed to work with any LLM provider (OpenAI, Claude, Ollama, etc.)
// by using standard JSON Schema conventions for parameter definitions.
//
// The Parameters field contains a JSON schema that describes the expected inputs
// for the tool. This schema is automatically translated to provider-specific formats
// by the LLM provider implementations.
//
// Example:
//
//	tool := pluginapi.Tool{
//	    Name:        "get_weather",
//	    Description: "Get the current weather for a location",
//	    Parameters: map[string]interface{}{
//	        "type": "object",
//	        "properties": map[string]interface{}{
//	            "location": map[string]interface{}{
//	                "type":        "string",
//	                "description": "The city and state, e.g. San Francisco, CA",
//	            },
//	            "unit": map[string]interface{}{
//	                "type":        "string",
//	                "description": "Temperature unit",
//	                "enum":        []string{"celsius", "fahrenheit"},
//	            },
//	        },
//	        "required": []string{"location"},
//	    },
//	}
type Tool struct {
	// Name is the unique identifier for this tool (required).
	// Should be descriptive and follow snake_case convention.
	Name string

	// Description explains what the tool does and when to use it (required).
	// This helps the LLM understand when to call this tool.
	Description string

	// Parameters is a JSON schema object that describes the tool's input parameters (required).
	// Must follow JSON Schema conventions. Use map[string]interface{} to represent
	// the schema structure flexibly.
	//
	// Common pattern:
	//   {
	//     "type": "object",
	//     "properties": { ... },
	//     "required": [ ... ]
	//   }
	Parameters map[string]interface{}
}
