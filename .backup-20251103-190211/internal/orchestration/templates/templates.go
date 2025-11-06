package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// WorkflowTemplate defines a reusable workflow pattern
type WorkflowTemplate struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Category      string                 `json:"category"`
	RequiredRoles []types.AgentRole      `json:"required_roles"`
	Parameters    []TemplateParameter    `json:"parameters"`
	Steps         []WorkflowStep         `json:"steps"`
	DefaultConfig map[string]interface{} `json:"default_config,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

// TemplateParameter defines a parameter that can be set when instantiating a template
type TemplateParameter struct {
	Name         string      `json:"name"`
	Type         string      `json:"type"` // string, number, boolean, array, object
	Description  string      `json:"description"`
	Required     bool        `json:"required"`
	DefaultValue interface{} `json:"default_value,omitempty"`
}

// WorkflowStep defines a single step in a workflow
type WorkflowStep struct {
	ID           string                 `json:"id"`
	Role         types.AgentRole        `json:"role"`
	Description  string                 `json:"description"`
	DependsOn    []string               `json:"depends_on,omitempty"`
	Priority     int                    `json:"priority"`
	Timeout      time.Duration          `json:"timeout"`
	Context      map[string]interface{} `json:"context,omitempty"`
}

// TemplateManager manages workflow templates
type TemplateManager struct {
	templatesDir string
	templates    map[string]*WorkflowTemplate
}

// NewTemplateManager creates a new template manager
func NewTemplateManager(templatesDir string) *TemplateManager {
	return &TemplateManager{
		templatesDir: templatesDir,
		templates:    make(map[string]*WorkflowTemplate),
	}
}

// LoadTemplates loads all templates from the templates directory
func (tm *TemplateManager) LoadTemplates() error {
	// Create templates directory if it doesn't exist
	if err := os.MkdirAll(tm.templatesDir, 0755); err != nil {
		return fmt.Errorf("failed to create templates directory: %w", err)
	}

	// Load built-in templates
	tm.loadBuiltInTemplates()

	// Load custom templates from disk
	files, err := os.ReadDir(tm.templatesDir)
	if err != nil {
		return fmt.Errorf("failed to read templates directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}

		templatePath := filepath.Join(tm.templatesDir, file.Name())
		data, err := os.ReadFile(templatePath)
		if err != nil {
			continue
		}

		var template WorkflowTemplate
		if err := json.Unmarshal(data, &template); err != nil {
			continue
		}

		tm.templates[template.ID] = &template
	}

	return nil
}

// loadBuiltInTemplates loads pre-built templates into memory
func (tm *TemplateManager) loadBuiltInTemplates() {
	// Research Pipeline Template
	tm.templates["research-pipeline"] = &WorkflowTemplate{
		ID:          "research-pipeline",
		Name:        "Research Pipeline",
		Description: "Comprehensive research workflow with analysis, synthesis, and validation",
		Category:    "research",
		RequiredRoles: []types.AgentRole{
			types.RoleResearcher,
			types.RoleAnalyzer,
			types.RoleSynthesizer,
		},
		Parameters: []TemplateParameter{
			{
				Name:        "topic",
				Type:        "string",
				Description: "Research topic or question",
				Required:    true,
			},
			{
				Name:        "depth",
				Type:        "string",
				Description: "Research depth (shallow, medium, deep)",
				Required:    false,
				DefaultValue: "medium",
			},
			{
				Name:        "include_validation",
				Type:        "boolean",
				Description: "Include validation step",
				Required:    false,
				DefaultValue: true,
			},
		},
		Steps: []WorkflowStep{
			{
				ID:          "research",
				Role:        types.RoleResearcher,
				Description: "Research: {{.topic}}",
				DependsOn:   []string{},
				Priority:    5,
				Timeout:     15 * time.Minute,
			},
			{
				ID:          "analysis",
				Role:        types.RoleAnalyzer,
				Description: "Analyze findings from research on: {{.topic}}",
				DependsOn:   []string{"research"},
				Priority:    4,
				Timeout:     10 * time.Minute,
			},
			{
				ID:          "synthesis",
				Role:        types.RoleSynthesizer,
				Description: "Create comprehensive report on: {{.topic}}",
				DependsOn:   []string{"research", "analysis"},
				Priority:    4,
				Timeout:     10 * time.Minute,
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Analysis Workflow Template
	tm.templates["data-analysis"] = &WorkflowTemplate{
		ID:          "data-analysis",
		Name:        "Data Analysis Workflow",
		Description: "Parallel data analysis with synthesis",
		Category:    "analysis",
		RequiredRoles: []types.AgentRole{
			types.RoleAnalyzer,
			types.RoleSynthesizer,
		},
		Parameters: []TemplateParameter{
			{
				Name:        "dataset",
				Type:        "string",
				Description: "Dataset or data source to analyze",
				Required:    true,
			},
			{
				Name:        "analysis_types",
				Type:        "array",
				Description: "Types of analysis to perform",
				Required:    false,
				DefaultValue: []string{"statistical", "qualitative"},
			},
		},
		Steps: []WorkflowStep{
			{
				ID:          "analyze",
				Role:        types.RoleAnalyzer,
				Description: "Analyze dataset: {{.dataset}}",
				DependsOn:   []string{},
				Priority:    5,
				Timeout:     20 * time.Minute,
			},
			{
				ID:          "synthesize",
				Role:        types.RoleSynthesizer,
				Description: "Synthesize analysis findings for: {{.dataset}}",
				DependsOn:   []string{"analyze"},
				Priority:    4,
				Timeout:     10 * time.Minute,
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Validation Workflow Template
	tm.templates["quality-validation"] = &WorkflowTemplate{
		ID:          "quality-validation",
		Name:        "Quality Validation Workflow",
		Description: "Multi-agent quality validation and verification",
		Category:    "validation",
		RequiredRoles: []types.AgentRole{
			types.RoleValidator,
			types.RoleAnalyzer,
		},
		Parameters: []TemplateParameter{
			{
				Name:        "content",
				Type:        "string",
				Description: "Content to validate",
				Required:    true,
			},
			{
				Name:        "criteria",
				Type:        "array",
				Description: "Validation criteria",
				Required:    false,
				DefaultValue: []string{"accuracy", "completeness", "consistency"},
			},
		},
		Steps: []WorkflowStep{
			{
				ID:          "validate",
				Role:        types.RoleValidator,
				Description: "Validate content: {{.content}}",
				DependsOn:   []string{},
				Priority:    5,
				Timeout:     10 * time.Minute,
			},
			{
				ID:          "analyze_validation",
				Role:        types.RoleAnalyzer,
				Description: "Analyze validation results for: {{.content}}",
				DependsOn:   []string{"validate"},
				Priority:    4,
				Timeout:     5 * time.Minute,
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Parallel Research Template
	tm.templates["parallel-research"] = &WorkflowTemplate{
		ID:          "parallel-research",
		Name:        "Parallel Research",
		Description: "Multiple researchers working in parallel with synthesis",
		Category:    "research",
		RequiredRoles: []types.AgentRole{
			types.RoleResearcher,
			types.RoleSynthesizer,
		},
		Parameters: []TemplateParameter{
			{
				Name:        "topics",
				Type:        "array",
				Description: "Array of topics to research",
				Required:    true,
			},
		},
		Steps: []WorkflowStep{
			{
				ID:          "research_parallel",
				Role:        types.RoleResearcher,
				Description: "Research topics: {{.topics}}",
				DependsOn:   []string{},
				Priority:    5,
				Timeout:     20 * time.Minute,
			},
			{
				ID:          "synthesize_findings",
				Role:        types.RoleSynthesizer,
				Description: "Synthesize parallel research findings",
				DependsOn:   []string{"research_parallel"},
				Priority:    4,
				Timeout:     15 * time.Minute,
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// GetTemplate retrieves a template by ID
func (tm *TemplateManager) GetTemplate(id string) (*WorkflowTemplate, error) {
	template, exists := tm.templates[id]
	if !exists {
		return nil, fmt.Errorf("template not found: %s", id)
	}
	return template, nil
}

// ListTemplates returns all available templates
func (tm *TemplateManager) ListTemplates() []*WorkflowTemplate {
	templates := make([]*WorkflowTemplate, 0, len(tm.templates))
	for _, template := range tm.templates {
		templates = append(templates, template)
	}
	return templates
}

// ListTemplatesByCategory returns templates filtered by category
func (tm *TemplateManager) ListTemplatesByCategory(category string) []*WorkflowTemplate {
	templates := make([]*WorkflowTemplate, 0)
	for _, template := range tm.templates {
		if template.Category == category {
			templates = append(templates, template)
		}
	}
	return templates
}

// SaveTemplate saves a template to disk
func (tm *TemplateManager) SaveTemplate(template *WorkflowTemplate) error {
	template.UpdatedAt = time.Now()
	if template.CreatedAt.IsZero() {
		template.CreatedAt = time.Now()
	}

	data, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal template: %w", err)
	}

	templatePath := filepath.Join(tm.templatesDir, template.ID+".json")
	if err := os.WriteFile(templatePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write template file: %w", err)
	}

	tm.templates[template.ID] = template
	return nil
}

// DeleteTemplate deletes a template
func (tm *TemplateManager) DeleteTemplate(id string) error {
	if _, exists := tm.templates[id]; !exists {
		return fmt.Errorf("template not found: %s", id)
	}

	templatePath := filepath.Join(tm.templatesDir, id+".json")
	if err := os.Remove(templatePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete template file: %w", err)
	}

	delete(tm.templates, id)
	return nil
}

// ValidateParameters validates parameters against template definition
func (tm *TemplateManager) ValidateParameters(template *WorkflowTemplate, params map[string]interface{}) error {
	for _, param := range template.Parameters {
		if param.Required {
			if _, exists := params[param.Name]; !exists {
				return fmt.Errorf("required parameter missing: %s", param.Name)
			}
		}
	}
	return nil
}

// InstantiateTemplate creates a workflow instance from a template with parameters
func (tm *TemplateManager) InstantiateTemplate(templateID string, params map[string]interface{}) (*WorkflowInstance, error) {
	template, err := tm.GetTemplate(templateID)
	if err != nil {
		return nil, err
	}

	// Validate parameters
	if err := tm.ValidateParameters(template, params); err != nil {
		return nil, err
	}

	// Merge with defaults
	finalParams := make(map[string]interface{})
	for _, param := range template.Parameters {
		if val, exists := params[param.Name]; exists {
			finalParams[param.Name] = val
		} else if param.DefaultValue != nil {
			finalParams[param.Name] = param.DefaultValue
		}
	}

	instance := &WorkflowInstance{
		TemplateID:    templateID,
		TemplateName:  template.Name,
		Parameters:    finalParams,
		RequiredRoles: template.RequiredRoles,
		Steps:         template.Steps,
		CreatedAt:     time.Now(),
	}

	return instance, nil
}

// WorkflowInstance represents an instantiated workflow from a template
type WorkflowInstance struct {
	TemplateID    string                 `json:"template_id"`
	TemplateName  string                 `json:"template_name"`
	Parameters    map[string]interface{} `json:"parameters"`
	RequiredRoles []types.AgentRole      `json:"required_roles"`
	Steps         []WorkflowStep         `json:"steps"`
	CreatedAt     time.Time              `json:"created_at"`
}
