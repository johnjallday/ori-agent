package orchestrationhttp

import (
	"testing"
)

func TestSubstituteInputPlaceholders(t *testing.T) {
	tests := []struct {
		name        string
		description string
		inputs      []string
		expected    string
	}{
		{
			name:        "Single numbered placeholder",
			description: "{input1} * 2",
			inputs:      []string{"4"},
			expected:    "4 * 2",
		},
		{
			name:        "Multiple numbered placeholders",
			description: "{input1} + {input2}",
			inputs:      []string{"4", "8"},
			expected:    "4 + 8",
		},
		{
			name:        "Previous shortcut",
			description: "multiply {previous} by 3",
			inputs:      []string{"5"},
			expected:    "multiply 5 by 3",
		},
		{
			name:        "Result shortcut",
			description: "{result} is the answer",
			inputs:      []string{"42"},
			expected:    "42 is the answer",
		},
		{
			name:        "No placeholders",
			description: "just text",
			inputs:      []string{"4"},
			expected:    "just text",
		},
		{
			name:        "No inputs",
			description: "{input1} test",
			inputs:      []string{},
			expected:    "{input1} test",
		},
		{
			name:        "Mixed placeholders",
			description: "Take {previous} and add {input2}",
			inputs:      []string{"10", "5"},
			expected:    "Take 10 and add 5",
		},
		{
			name:        "Multiple occurrences of same placeholder",
			description: "{input1} + {input1} equals double {input1}",
			inputs:      []string{"7"},
			expected:    "7 + 7 equals double 7",
		},
		{
			name:        "Three inputs",
			description: "{input1}, {input2}, and {input3}",
			inputs:      []string{"first", "second", "third"},
			expected:    "first, second, and third",
		},
		{
			name:        "Natural language with context",
			description: "What is the population of that city?",
			inputs:      []string{"Paris"},
			expected:    "What is the population of that city?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteInputPlaceholders(tt.description, tt.inputs)
			if result != tt.expected {
				t.Errorf("substituteInputPlaceholders() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSubstituteInputPlaceholders_EdgeCases(t *testing.T) {
	t.Run("Empty description", func(t *testing.T) {
		result := substituteInputPlaceholders("", []string{"test"})
		if result != "" {
			t.Errorf("Expected empty string, got %q", result)
		}
	})

	t.Run("Placeholder with no matching input", func(t *testing.T) {
		result := substituteInputPlaceholders("{input5}", []string{"1", "2"})
		if result != "{input5}" {
			t.Errorf("Expected unchanged placeholder, got %q", result)
		}
	})

	t.Run("Both shortcuts with same input", func(t *testing.T) {
		result := substituteInputPlaceholders("{previous} and {result}", []string{"same"})
		expected := "same and same"
		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("Numeric input values", func(t *testing.T) {
		result := substituteInputPlaceholders("{input1} + {input2} = ?", []string{"3", "7"})
		expected := "3 + 7 = ?"
		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("Input with special characters", func(t *testing.T) {
		result := substituteInputPlaceholders("Result: {input1}", []string{"$100.50"})
		expected := "Result: $100.50"
		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("Input with curly braces", func(t *testing.T) {
		result := substituteInputPlaceholders("{input1}", []string{"{nested}"})
		expected := "{nested}"
		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("Long input text", func(t *testing.T) {
		longText := "This is a very long text that represents a complete task result with multiple sentences and detailed information."
		result := substituteInputPlaceholders("Summary: {input1}", []string{longText})
		expected := "Summary: " + longText
		if result != expected {
			t.Errorf("Long text substitution failed")
		}
	})
}
