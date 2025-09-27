package main

import (
	"fmt"
	"log"

	"github.com/johnjallday/dolphin-agent/internal/pluginhttp"
	"github.com/openai/openai-go/v2"
)

// Example function demonstrating how to use the EnumExtractor
func main() {
	// Create an EnumExtractor instance
	extractor := pluginhttp.NewEnumExtractor()

	// Example function definition that matches the math.go structure
	mathDefinition := openai.FunctionDefinitionParam{
		Name:        "math",
		Description: openai.String("Perform basic math operations"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "string",
					"description": "Operation to perform",
					"enum":        []string{"add", "subtract", "multiply", "divide"},
				},
				"a": map[string]any{
					"type":        "number",
					"description": "First operand",
				},
				"b": map[string]any{
					"type":        "number",
					"description": "Second operand",
				},
			},
			"required": []string{"operation", "a", "b"},
		},
	}

	// Example 1: Extract specific property enum
	fmt.Println("=== Example 1: Extract operation enum ===")
	operationEnums, err := extractor.ExtractEnumsFromParameter(mathDefinition, "operation")
	if err != nil {
		log.Printf("Error extracting operation enums: %v", err)
	} else {
		fmt.Printf("Operation enum values: %v\n", operationEnums)
	}

	// Example 2: Get all enums from the function
	fmt.Println("\n=== Example 2: Get all enums ===")
	allEnums, err := extractor.GetAllEnumsFromParameter(mathDefinition)
	if err != nil {
		log.Printf("Error getting all enums: %v", err)
	} else {
		for propertyName, enumValues := range allEnums {
			fmt.Printf("Property '%s' has enum values: %v\n", propertyName, enumValues)
		}
	}

	// Example 3: Validate enum values
	fmt.Println("\n=== Example 3: Validate enum values ===")
	testValues := []string{"add", "subtract", "invalid_operation", "multiply"}

	for _, value := range testValues {
		isValid, err := extractor.ValidateEnumValue(mathDefinition, "operation", value)
		if err != nil {
			log.Printf("Error validating %s: %v", value, err)
		} else {
			status := "❌ Invalid"
			if isValid {
				status = "✅ Valid"
			}
			fmt.Printf("Value '%s': %s\n", value, status)
		}
	}

	// Example 4: Try to extract from non-enum property
	fmt.Println("\n=== Example 4: Extract from non-enum property ===")
	_, err = extractor.ExtractEnumsFromParameter(mathDefinition, "a")
	if err != nil {
		fmt.Printf("Expected error for non-enum property 'a': %v\n", err)
	}
}