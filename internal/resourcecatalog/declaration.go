// Package resourcecatalog holds the host side of a resource workspace's
// catalog: the declaration a project plugin names it with, the wire answers of
// its two read-only operations, and (later) the Studio-owned catalog files.
// The contract is docs/architecture/reaper-studio.md. Host code here is
// domain-neutral: every word a person reads comes from the declaration.
package resourcecatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

const (
	// DeclarationSchemaVersion is the only `resource_catalog` schema v1 accepts.
	DeclarationSchemaVersion = 1
	// SupportedCatalogVersion is the catalog definition version this host reads.
	// A newer one is a new contract, not a partially understood one.
	SupportedCatalogVersion = 1

	MaxCategories = 8
	MaxKinds      = 8
	MaxFacts      = 8

	maxDeclarationBytes = 64 << 10
	maxLabelChars       = 40
	maxDescriptionChars = 240
)

var (
	// ErrInvalidDeclaration is any `resource_catalog` the host cannot accept
	// whole. Nothing of a refused declaration is used.
	ErrInvalidDeclaration = errors.New("resource catalog declaration is invalid")

	componentIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	operationIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// Category, kind, fact and source IDs are part of every stored entry and
	// every agent answer, so they stay short and boring.
	localIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

// Declaration is a manifest's top-level `resource_catalog`. It names the
// resource blueprint it belongs to, the two read-only operations that report
// sources and inventory, and the words the owner view is rendered with.
type Declaration struct {
	SchemaVersion      int        `json:"schema_version"`
	ID                 string     `json:"id"`
	Version            int        `json:"version"`
	BlueprintID        string     `json:"blueprint_id"`
	ServiceID          string     `json:"service_id"`
	SourcesOperation   string     `json:"sources_operation"`
	InventoryOperation string     `json:"inventory_operation"`
	Categories         []Category `json:"categories"`
}

// Category is one browsable group of resources, such as templates.
type Category struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Kinds       []Kind      `json:"kinds"`
	Facts       []FactField `json:"facts"`
}

// Kind is one type of entry inside a category.
type Kind struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// FactField is one observed fact an entry of the category may report. An
// entry that does not report it is shown as Unknown, never guessed.
type FactField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// DecodeDeclaration accepts one `resource_catalog` value whole or not at all.
// It checks the declaration's own shape; whether its blueprint and operations
// exist in the same contribution is the plugin validator's job.
func DecodeDeclaration(raw []byte) (*Declaration, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("%w: missing", ErrInvalidDeclaration)
	}
	var declaration Declaration
	if err := workspacecontinuity.DecodeDocument(trimmed, &declaration, maxDeclarationBytes); err != nil {
		return nil, fmt.Errorf("%w: not the declared shape", ErrInvalidDeclaration)
	}
	if err := declaration.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDeclaration, err)
	}
	return &declaration, nil
}

func (d *Declaration) validate() error {
	switch {
	case d.SchemaVersion != DeclarationSchemaVersion:
		return errors.New("schema_version must be 1")
	case !componentIDPattern.MatchString(d.ID):
		return errors.New("id is not a stable identifier")
	case d.Version != SupportedCatalogVersion:
		return errors.New("version is not supported by this host")
	case !componentIDPattern.MatchString(d.BlueprintID):
		return errors.New("blueprint_id is not a stable identifier")
	case !componentIDPattern.MatchString(d.ServiceID):
		return errors.New("service_id is not a stable identifier")
	case !operationIDPattern.MatchString(d.SourcesOperation) || !operationIDPattern.MatchString(d.InventoryOperation):
		return errors.New("operations must be operation identifiers")
	case d.SourcesOperation == d.InventoryOperation:
		return errors.New("sources and inventory must be different operations")
	case len(d.Categories) == 0 || len(d.Categories) > MaxCategories:
		return fmt.Errorf("categories must number 1 to %d", MaxCategories)
	}
	seen := make(map[string]bool, len(d.Categories))
	for _, category := range d.Categories {
		if !localIDPattern.MatchString(category.ID) || seen[category.ID] {
			return errors.New("category ids must be unique identifiers")
		}
		seen[category.ID] = true
		if !plainLine(category.Label, maxLabelChars) || !plainLine(category.Description, maxDescriptionChars) {
			return errors.New("category label or description is not one plain line within its limit")
		}
		if len(category.Kinds) == 0 || len(category.Kinds) > MaxKinds {
			return fmt.Errorf("a category declares 1 to %d kinds", MaxKinds)
		}
		kinds := make(map[string]bool, len(category.Kinds))
		for _, kind := range category.Kinds {
			if !localIDPattern.MatchString(kind.ID) || kinds[kind.ID] || !plainLine(kind.Label, maxLabelChars) {
				return errors.New("kinds must be unique identifiers with a plain label")
			}
			kinds[kind.ID] = true
		}
		// A nil list is refused as well as an oversized one: the declaration
		// says "no facts" with [], so a forgotten key is not read as a choice.
		if category.Facts == nil || len(category.Facts) > MaxFacts {
			return fmt.Errorf("a category declares 0 to %d facts", MaxFacts)
		}
		facts := make(map[string]bool, len(category.Facts))
		for _, fact := range category.Facts {
			if !localIDPattern.MatchString(fact.ID) || facts[fact.ID] || !plainLine(fact.Label, maxLabelChars) {
				return errors.New("facts must be unique identifiers with a plain label")
			}
			facts[fact.ID] = true
		}
	}
	return nil
}

// Category returns the declared category with id.
func (d *Declaration) Category(id string) (Category, bool) {
	if d == nil {
		return Category{}, false
	}
	for _, category := range d.Categories {
		if category.ID == id {
			return category, true
		}
	}
	return Category{}, false
}

func (c Category) hasKind(id string) bool {
	for _, kind := range c.Kinds {
		if kind.ID == id {
			return true
		}
	}
	return false
}

func (c Category) hasFact(id string) bool {
	for _, fact := range c.Facts {
		if fact.ID == id {
			return true
		}
	}
	return false
}

// MarshalJSON keeps an empty fact list as [] so a stored declaration decodes
// back through the same strict rule it was accepted under.
func (c Category) MarshalJSON() ([]byte, error) {
	type plain Category
	if c.Facts == nil {
		c.Facts = []FactField{}
	}
	return json.Marshal(plain(c))
}
