package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// YAMLToolParameter represents a parameter in plugin.yaml
type YAMLToolParameter struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required,omitempty"`
	Enum        []string `yaml:"enum,omitempty"`
}

// YAMLOperationDefinition represents per-operation parameters in plugin.yaml
type YAMLOperationDefinition struct {
	Parameters []YAMLToolParameter `yaml:"parameters"`
}

// YAMLToolDefinition represents tool definition in plugin.yaml
type YAMLToolDefinition struct {
	Name        string                             `yaml:"name"`
	Description string                             `yaml:"description"`
	Parameters  []YAMLToolParameter                `yaml:"parameters"`
	Operations  map[string]YAMLOperationDefinition `yaml:"operations,omitempty"`
}

// Maintainer represents a plugin maintainer
type Maintainer struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// Requirements represents plugin requirements
type Requirements struct {
	MinOriVersion string   `yaml:"min_ori_version"`
	Dependencies  []string `yaml:"dependencies"`
}

// ConfigVariable represents a configuration variable
type ConfigVariable struct {
	Key          string `yaml:"key"`
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	Type         string `yaml:"type"`
	Required     bool   `yaml:"required"`
	DefaultValue string `yaml:"default_value"`
}

// PluginConfigSection represents the config section
type PluginConfigSection struct {
	Variables []ConfigVariable `yaml:"variables"`
}

// PluginConfig minimal representation
type PluginConfig struct {
	Name         string               `yaml:"name"`
	Version      string               `yaml:"version"`
	License      string               `yaml:"license"`
	Repository   string               `yaml:"repository"`
	Maintainers  []Maintainer         `yaml:"maintainers"`
	Requirements *Requirements        `yaml:"requirements,omitempty"`
	Config       *PluginConfigSection `yaml:"config,omitempty"`
	Tool         *YAMLToolDefinition  `yaml:"tool_definition,omitempty"`
}

// TemplateData holds data for code generation template
type TemplateData struct {
	PackageName        string
	ToolName           string
	ParamsStruct       string
	Fields             []FieldInfo
	OptionalInterfaces []string // Optional interfaces to generate checks for
}

type FieldInfo struct {
	Name    string
	Type    string
	JSONTag string
	Comment string
}

func main() {
	yamlFile := flag.String("yaml", "plugin.yaml", "Path to plugin.yaml file")
	output := flag.String("output", "", "Output file (default: <tool>_generated.go)")
	pkg := flag.String("package", "main", "Package name for generated code")
	flag.Parse()

	// Read and parse plugin.yaml
	data, err := os.ReadFile(*yamlFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", *yamlFile, err)
		os.Exit(1)
	}

	var config PluginConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", *yamlFile, err)
		os.Exit(1)
	}

	if config.Tool == nil {
		fmt.Fprintf(os.Stderr, "No tool_definition found in %s\n", *yamlFile)
		os.Exit(1)
	}

	// Determine output file
	outputFile := *output
	if outputFile == "" {
		toolName := strings.ReplaceAll(config.Name, "-", "_")
		outputFile = fmt.Sprintf("%s_generated.go", toolName)
	}

	// Generate code
	code, err := generateCode(*pkg, &config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating code: %v\n", err)
		os.Exit(1)
	}

	// Format the generated code
	formatted, err := format.Source([]byte(code))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting code: %v\n", err)
		fmt.Fprintf(os.Stderr, "Generated code:\n%s\n", code)
		os.Exit(1)
	}

	// Write output
	if err := os.WriteFile(outputFile, formatted, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", outputFile, err)
		os.Exit(1)
	}

	fmt.Printf("✓ Generated %s from %s\n", outputFile, *yamlFile)
}

// detectOptionalInterfaces determines which optional interfaces to generate based on plugin.yaml
func detectOptionalInterfaces(config *PluginConfig) []string {
	var interfaces []string

	// VersionedTool: if version is specified
	if config.Version != "" {
		interfaces = append(interfaces, "pluginapi.VersionedTool")
	}

	// MetadataProvider: if maintainers, license, or repository are specified
	if len(config.Maintainers) > 0 || config.License != "" || config.Repository != "" {
		interfaces = append(interfaces, "pluginapi.MetadataProvider")
	}

	// PluginCompatibility: if min_ori_version is specified
	if config.Requirements != nil && config.Requirements.MinOriVersion != "" {
		interfaces = append(interfaces, "pluginapi.PluginCompatibility")
	}

	// InitializationProvider: if config variables are specified
	if config.Config != nil && len(config.Config.Variables) > 0 {
		interfaces = append(interfaces, "pluginapi.InitializationProvider")
	}

	// Note: AgentAwareTool and WebPageProvider require manual implementation
	// and cannot be auto-detected from YAML, so they're not included here

	return interfaces
}

func generateCode(pkgName string, config *PluginConfig) (string, error) {
	// Build template data
	toolName := strings.ReplaceAll(config.Name, "-", "_")
	paramsStruct := toPascalCase(toolName) + "Params"

	var fields []FieldInfo
	params, err := collectParameters(config.Tool)
	if err != nil {
		return "", err
	}

	// Convert parameters to struct fields
	for _, param := range params {
		fieldName := toPascalCase(param.Name)
		goType := yamlTypeToGoType(param.Type)

		field := FieldInfo{
			Name:    fieldName,
			Type:    goType,
			JSONTag: param.Name,
			Comment: param.Description,
		}
		fields = append(fields, field)
	}

	// Detect optional interfaces based on plugin.yaml content
	optionalInterfaces := detectOptionalInterfaces(config)

	tmplData := TemplateData{
		PackageName:        pkgName,
		ToolName:           toolName,
		ParamsStruct:       paramsStruct,
		Fields:             fields,
		OptionalInterfaces: optionalInterfaces,
	}

	// Execute template
	var buf bytes.Buffer
	if err := codeTemplate.Execute(&buf, tmplData); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func collectParameters(tool *YAMLToolDefinition) ([]YAMLToolParameter, error) {
	if tool == nil {
		return nil, fmt.Errorf("tool definition is nil")
	}

	seen := make(map[string]YAMLToolParameter)
	var ordered []YAMLToolParameter

	addParam := func(param YAMLToolParameter) error {
		if param.Name == "" {
			return fmt.Errorf("parameter name is required")
		}
		if existing, ok := seen[param.Name]; ok {
			if existing.Type != param.Type {
				return fmt.Errorf("parameter %q has conflicting types: %s vs %s", param.Name, existing.Type, param.Type)
			}
			return nil
		}
		seen[param.Name] = param
		ordered = append(ordered, param)
		return nil
	}

	for _, param := range tool.Parameters {
		if err := addParam(param); err != nil {
			return nil, err
		}
	}

	if len(tool.Operations) > 0 {
		opNames := make([]string, 0, len(tool.Operations))
		for name := range tool.Operations {
			opNames = append(opNames, name)
		}
		sort.Strings(opNames)

		for _, name := range opNames {
			op := tool.Operations[name]
			for _, param := range op.Parameters {
				if err := addParam(param); err != nil {
					return nil, err
				}
			}
		}
	}

	return ordered, nil
}

func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

func yamlTypeToGoType(yamlType string) string {
	switch yamlType {
	case "string":
		return "string"
	case "integer":
		return "int"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		return "[]interface{}"
	case "object":
		return "map[string]interface{}"
	default:
		return "interface{}"
	}
}

var codeTemplate = template.Must(template.New("plugin").Parse(`// Code generated by ori-plugin-gen. DO NOT EDIT.

package {{.PackageName}}

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// Compile-time interface checks
var _ pluginapi.PluginTool = (*{{.ToolName}}Tool)(nil)
{{- if .OptionalInterfaces}}

// Optional interface checks (auto-detected from plugin.yaml)
var (
{{- range .OptionalInterfaces}}
	_ {{.}} = (*{{$.ToolName}}Tool)(nil)
{{- end}}
)
{{- end}}

// {{.ParamsStruct}} represents the parameters for this plugin
type {{.ParamsStruct}} struct {
{{- range .Fields}}
	{{.Name}} {{.Type}} ` + "`json:\"{{.JSONTag}}\"`" + ` // {{.Comment}}
{{- end}}
}

// Call implements the PluginTool interface
// This method is auto-generated from plugin.yaml
func (t *{{.ToolName}}Tool) Call(ctx context.Context, args string) (string, error) {
	var paramsMap map[string]interface{}

	// Unmarshal JSON arguments for validation
	if err := json.Unmarshal([]byte(args), &paramsMap); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if err := pluginapi.ValidateToolParameters(t.Definition().Parameters, paramsMap); err != nil {
		return "", err
	}

	var params {{.ParamsStruct}}

	// Unmarshal JSON arguments into typed params
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Call the Execute method (implemented by you)
	return t.Execute(ctx, &params)
}
`))
