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
	Validation   string `yaml:"validation,omitempty"` // regex pattern
	Min          *int   `yaml:"min,omitempty"`        // min value for numeric
	Max          *int   `yaml:"max,omitempty"`        // max value for numeric
}

// PluginConfigSection represents the config section
type PluginConfigSection struct {
	Variables []ConfigVariable `yaml:"variables"`
}

// AcceptsFilesSection represents the accepts_files section in plugin.yaml
type AcceptsFilesSection struct {
	Extensions     []string `yaml:"extensions"`
	MimeTypes      []string `yaml:"mime_types,omitempty"`
	FileOperations []string `yaml:"file_operations,omitempty"` // operations that have WithFiles handlers
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
	AcceptsFiles *AcceptsFilesSection `yaml:"accepts_files,omitempty"`
	Assets       []string             `yaml:"assets,omitempty"`
	WebPages     []string             `yaml:"web_pages,omitempty"`
}

// TemplateData holds data for code generation template
type TemplateData struct {
	PackageName        string
	ToolName           string
	ToolNamePascal     string // PascalCase version of tool name
	ParamsStruct       string
	Fields             []FieldInfo
	OptionalInterfaces []string // Optional interfaces to generate checks for

	// Operation handler generation
	Operations    []OperationInfo
	HasOperations bool

	// Config generation
	ConfigVars    []ConfigVariable
	HasConfig     bool
	HasValidation bool // true if any config var has validation pattern

	// File attachment generation
	AcceptsFiles      []string // File extensions
	HasAcceptsFiles   bool
	FileOperations    []OperationInfo // Operations with WithFiles handlers
	HasFileOperations bool

	// Web page generation
	WebPages        []string
	WebPageHandlers []OperationInfo // Web page handlers (reusing OperationInfo)
	HasWebPages     bool

	// Template embedding
	Assets    []string
	HasAssets bool
}

// OperationInfo holds info about an operation for code generation
type OperationInfo struct {
	Name        string // operation name from yaml (e.g., "create_project")
	HandlerName string // handler function name (e.g., "handleCreateProject")
}

type FieldInfo struct {
	Name    string
	Type    string
	JSONTag string
	Comment string
}

// buildWebPageHandlers creates handler info for web pages
func buildWebPageHandlers(pages []string) []OperationInfo {
	var handlers []OperationInfo
	for _, page := range pages {
		handlers = append(handlers, OperationInfo{
			Name:        page,
			HandlerName: "serve" + toPascalCase(page) + "Page",
		})
	}
	return handlers
}

// getOperationNames extracts sorted operation names from tool definition
func getOperationNames(tool *YAMLToolDefinition) []string {
	if tool == nil || len(tool.Operations) == 0 {
		return nil
	}
	names := make([]string, 0, len(tool.Operations))
	for name := range tool.Operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

	// FileAttachmentHandler: if accepts_files is specified
	if config.AcceptsFiles != nil && len(config.AcceptsFiles.Extensions) > 0 {
		interfaces = append(interfaces, "pluginapi.FileAttachmentHandler")
	}

	// WebPageProvider: if web_pages is specified
	if len(config.WebPages) > 0 {
		interfaces = append(interfaces, "pluginapi.WebPageProvider")
	}

	// Note: AgentAwareTool requires manual implementation

	return interfaces
}

func generateCode(pkgName string, config *PluginConfig) (string, error) {
	// Build template data
	toolName := strings.ReplaceAll(config.Name, "-", "_")
	toolNamePascal := toPascalCase(toolName)
	paramsStruct := "Params"

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

	// Build operation info for handler generation
	var operations []OperationInfo
	opNames := getOperationNames(config.Tool)
	for _, name := range opNames {
		operations = append(operations, OperationInfo{
			Name:        name,
			HandlerName: "handle" + toPascalCase(name),
		})
	}

	// Collect config variables and check for validation patterns
	var configVars []ConfigVariable
	var hasValidation bool
	if config.Config != nil {
		configVars = config.Config.Variables
		for _, v := range configVars {
			if v.Validation != "" {
				hasValidation = true
				break
			}
		}
	}

	// Collect file extensions and file operations
	var acceptsFiles []string
	var fileOperations []OperationInfo
	if config.AcceptsFiles != nil {
		acceptsFiles = config.AcceptsFiles.Extensions
		// Build file operation info for handler generation
		for _, opName := range config.AcceptsFiles.FileOperations {
			fileOperations = append(fileOperations, OperationInfo{
				Name:        opName,
				HandlerName: "handle" + toPascalCase(opName) + "WithFiles",
			})
		}
	}

	tmplData := TemplateData{
		PackageName:        pkgName,
		ToolName:           toolName,
		ToolNamePascal:     toolNamePascal,
		ParamsStruct:       paramsStruct,
		Fields:             fields,
		OptionalInterfaces: optionalInterfaces,

		// Operations
		Operations:    operations,
		HasOperations: len(operations) > 0,

		// Config
		ConfigVars:    configVars,
		HasConfig:     len(configVars) > 0,
		HasValidation: hasValidation,

		// File attachments
		AcceptsFiles:      acceptsFiles,
		HasAcceptsFiles:   len(acceptsFiles) > 0,
		FileOperations:    fileOperations,
		HasFileOperations: len(fileOperations) > 0,

		// Web pages
		WebPages:        config.WebPages,
		WebPageHandlers: buildWebPageHandlers(config.WebPages),
		HasWebPages:     len(config.WebPages) > 0,

		// Template embedding
		Assets:    config.Assets,
		HasAssets: len(config.Assets) > 0,
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
	// Replace hyphens with underscores for consistent splitting
	s = strings.ReplaceAll(s, "-", "_")
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
{{- if .HasAssets}}
	"embed"
{{- else}}
	_ "embed"
{{- end}}
	"context"
	"encoding/json"
	"fmt"
{{- if .HasValidation}}
	"regexp"
{{- end}}

	"github.com/oriagent/ori-pluginapi"
)

// Compile-time interface checks
var _ pluginapi.PluginTool = (*{{.ToolNamePascal}}Tool)(nil)
{{- if .OptionalInterfaces}}

// Optional interface checks (auto-detected from plugin.yaml)
var (
{{- range .OptionalInterfaces}}
	_ {{.}} = (*{{$.ToolNamePascal}}Tool)(nil)
{{- end}}
)
{{- end}}

//go:embed plugin.yaml
var configYAML string

{{- if .HasAssets}}
{{- range .Assets}}
//go:embed {{.}}
{{- end}}
var assetsFS embed.FS

{{- end}}
// {{.ParamsStruct}} represents the parameters for this plugin
type {{.ParamsStruct}} struct {
{{- range .Fields}}
	{{.Name}} {{.Type}} ` + "`json:\"{{.JSONTag}}\"`" + ` // {{.Comment}}
{{- end}}
}
{{- if .HasOperations}}

// OperationHandler is a function that handles a specific operation
type OperationHandler func(ctx context.Context, t *{{.ToolNamePascal}}Tool, params *{{.ParamsStruct}}) (string, error)

// operationRegistry maps operation names to their handler functions
// Handler functions must be defined by you with the naming convention handle{PascalCase}
var operationRegistry = map[string]OperationHandler{
{{- range .Operations}}
	"{{.Name}}": {{.HandlerName}},
{{- end}}
}

// Execute dispatches to the appropriate operation handler
// This method is auto-generated from plugin.yaml operations
func (t *{{.ToolNamePascal}}Tool) Execute(ctx context.Context, params *{{.ParamsStruct}}) (string, error) {
	handler, ok := operationRegistry[params.Operation]
	if !ok {
		return "", fmt.Errorf("unknown operation: %s. Valid operations: {{range $i, $op := .Operations}}{{if $i}}, {{end}}{{$op.Name}}{{end}}", params.Operation)
	}
	return handler(ctx, t, params)
}
{{- end}}

// Call implements the PluginTool interface
// This method is auto-generated from plugin.yaml
func (t *{{.ToolNamePascal}}Tool) Call(ctx context.Context, args string) (string, error) {
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

	// Call the Execute method
	return t.Execute(ctx, &params)
}
{{- if .HasConfig}}

// GetRequiredConfig returns the configuration variables needed by this plugin
// This method is auto-generated from plugin.yaml config section
func (t *{{.ToolNamePascal}}Tool) GetRequiredConfig() []pluginapi.ConfigVariable {
	return t.GetConfigFromYAML()
}

// ValidateConfig validates the provided configuration
// This method is auto-generated from plugin.yaml config section
func (t *{{.ToolNamePascal}}Tool) ValidateConfig(config map[string]interface{}) error {
{{- range .ConfigVars}}
{{- if .Required}}
	// Validate {{.Key}} (required)
	if val, ok := config["{{.Key}}"]; !ok || val == nil || val == "" {
		return fmt.Errorf("{{.Key}} is required")
	}
{{- end}}
{{- if .Validation}}
	// Validate {{.Key}} pattern
	if val, ok := config["{{.Key}}"].(string); ok && val != "" {
		if matched, _ := regexp.MatchString(` + "`{{.Validation}}`" + `, val); !matched {
			return fmt.Errorf("{{.Key}} does not match required pattern")
		}
	}
{{- end}}
{{- end}}
	return nil
}

// InitializeWithConfig initializes the plugin with the provided configuration
// This method is auto-generated from plugin.yaml config section
func (t *{{.ToolNamePascal}}Tool) InitializeWithConfig(config map[string]interface{}) error {
	sm := t.Settings()
	if sm == nil {
		return fmt.Errorf("settings manager not available")
	}
	for key, value := range config {
		if err := sm.Set(key, value); err != nil {
			return fmt.Errorf("failed to store config %s: %w", key, err)
		}
	}
	return nil
}
{{- end}}
{{- if .HasAcceptsFiles}}

// AcceptsFiles returns the list of file types this plugin accepts
// This method is auto-generated from plugin.yaml accepts_files section
func (t *{{.ToolNamePascal}}Tool) AcceptsFiles() []string {
	return []string{
{{- range .AcceptsFiles}}
		"{{.}}",
{{- end}}
	}
}
{{- if .HasFileOperations}}

// FileOperationHandler is a function that handles a specific operation with file attachments
type FileOperationHandler func(ctx context.Context, t *{{.ToolNamePascal}}Tool, params *{{.ParamsStruct}}, files []pluginapi.FileAttachment) (string, error)

// fileOperationRegistry maps operation names to their file handler functions
// Handler functions must be defined by you with the naming convention handle{PascalCase}WithFiles
var fileOperationRegistry = map[string]FileOperationHandler{
{{- range .FileOperations}}
	"{{.Name}}": {{.HandlerName}},
{{- end}}
}

// CallWithFiles handles file attachments by dispatching to file operation handlers
// This method is auto-generated from plugin.yaml accepts_files.file_operations section
func (t *{{.ToolNamePascal}}Tool) CallWithFiles(ctx context.Context, args string, files []pluginapi.FileAttachment) (string, error) {
	var params {{.ParamsStruct}}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Check if this operation has a file handler
	if handler, ok := fileOperationRegistry[params.Operation]; ok {
		return handler(ctx, t, &params, files)
	}

	// Fall back to regular Execute for operations without file handlers
	return t.Execute(ctx, &params)
}
{{- else}}

// NOTE: CallWithFiles must be implemented manually in main.go
// Default implementation (copy if you don't need custom file handling):
//
// func (t *{{.ToolNamePascal}}Tool) CallWithFiles(ctx context.Context, args string, files []pluginapi.FileAttachment) (string, error) {
//     var params {{.ParamsStruct}}
//     if err := json.Unmarshal([]byte(args), &params); err != nil {
//         return "", fmt.Errorf("invalid arguments: %w", err)
//     }
//     return t.Execute(ctx, &params)
// }
{{- end}}
{{- end}}
{{- if .HasWebPages}}

// GetWebPages returns the available web pages for this plugin
// This method is auto-generated from plugin.yaml web_pages section
func (t *{{.ToolNamePascal}}Tool) GetWebPages() []string {
	return []string{
{{- range .WebPages}}
		"{{.}}",
{{- end}}
	}
}

// WebPageHandler is a function that serves a specific web page
type WebPageHandler func(t *{{.ToolNamePascal}}Tool, query map[string]string) (string, string, error)

// webPageRegistry maps page paths to their handler functions
// Handler functions must be defined by you with the naming convention serve{PascalCase}Page
var webPageRegistry = map[string]WebPageHandler{
{{- range .WebPageHandlers}}
	"{{.Name}}": {{.HandlerName}},
{{- end}}
}

// ServeWebPage dispatches to the appropriate page handler
// This method is auto-generated from plugin.yaml web_pages section
func (t *{{.ToolNamePascal}}Tool) ServeWebPage(path string, query map[string]string) (string, string, error) {
	handler, ok := webPageRegistry[path]
	if !ok {
		return "", "", fmt.Errorf("page not found: %s", path)
	}
	return handler(t, query)
}
{{- end}}
`))
