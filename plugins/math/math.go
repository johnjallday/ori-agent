package main

import (
   "context"
   "encoding/json"
   "errors"
   "fmt"

   "github.com/johnjallday/dolphin-agent/pluginapi"
   "github.com/openai/openai-go/v2"
)

// mathTool implements pluginapi.Tool for basic arithmetic operations.
type mathTool struct{}

// ensure mathTool implements pluginapi.Tool at compile time
var _ pluginapi.Tool = (*mathTool)(nil)

// Definition returns the OpenAI function definition for the math operation.
func (m *mathTool) Definition() openai.FunctionDefinitionParam {
   return openai.FunctionDefinitionParam{
       Name:        "math",
       Description: openai.String("Perform basic math operations: add, subtract, multiply, divide"),
       Parameters: openai.FunctionParameters{
           "type": "object",
           "properties": map[string]any{
               "operation": map[string]any{
                   "type":        "string",
                   "description": "Operation to perform: add, subtract, multiply, divide",
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
}

// Call is invoked with the function arguments and returns the computed result.
func (m *mathTool) Call(ctx context.Context, args string) (string, error) {
   var p struct {
       Operation string  `json:"operation"`
       A         float64 `json:"a"`
       B         float64 `json:"b"`
   }
   if err := json.Unmarshal([]byte(args), &p); err != nil {
       return "", err
   }
   var result float64
   switch p.Operation {
   case "add":
       result = p.A + p.B
   case "subtract":
       result = p.A - p.B
   case "multiply":
       result = p.A * p.B
   case "divide":
       if p.B == 0 {
           return "", errors.New("division by zero")
       }
       result = p.A / p.B
   default:
       return "", fmt.Errorf("unknown operation %q", p.Operation)
   }
   return fmt.Sprintf("%g", result), nil
}

// Tool is the exported symbol that the host application will look up.
var Tool mathTool
