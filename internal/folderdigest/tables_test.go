package folderdigest_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

func TestCapabilityTableIntegrity(t *testing.T) {
	library := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(library); err != nil {
		t.Fatal(err)
	}
	seenMarkers := map[string]folderdigest.Shape{}
	seenExtensions := map[string]folderdigest.Shape{}
	seenShapes := map[folderdigest.Shape]bool{}
	for _, row := range folderdigest.AllCapabilities() {
		if row.Shape != "" {
			if seenShapes[row.Shape] {
				t.Errorf("duplicate shape %q", row.Shape)
			}
			seenShapes[row.Shape] = true
		}
		for _, marker := range row.Markers {
			if marker.Shape != row.Shape {
				t.Errorf("marker %q has shape %q, want %q", marker.Name, marker.Shape, row.Shape)
			}
			if previous, ok := seenMarkers[marker.Name]; ok {
				t.Errorf("marker %q claimed by both %q and %q", marker.Name, previous, row.Shape)
			}
			seenMarkers[marker.Name] = row.Shape
			if marker.Kind == folderdigest.MarkerGlob && strings.HasPrefix(marker.Name, "*.") {
				claimExtension(t, seenExtensions, marker.Name[1:], row.Shape)
			}
		}
		for _, tool := range row.Tools {
			if tool.Match == folderdigest.ToolByExtension {
				claimExtension(t, seenExtensions, tool.Value, row.Shape)
			}
		}
		if row.Blueprint.BlueprintID != "" {
			if row.Blueprint.Shape != row.Shape {
				t.Errorf("blueprint %q shape mismatch", row.Blueprint.BlueprintID)
			}
			if _, err := projecttemplates.FindLibraryTemplate(library, row.Blueprint.BlueprintID); err != nil {
				// Reviewed plugin blueprints are not part of the built-in library.
				if row.Offer == nil {
					t.Errorf("unresolved blueprint %q: %v", row.Blueprint.BlueprintID, err)
				} else if entry, ok := reviewedintegration.Get(row.Offer.IntegrationKey); !ok || entry.ExpectedBlueprintID != row.Blueprint.BlueprintID {
					t.Errorf("unresolved reviewed blueprint %q", row.Blueprint.BlueprintID)
				}
			}
		}
		if row.Offer == nil {
			continue
		}
		o := row.Offer
		entry, ok := reviewedintegration.Get(o.IntegrationKey)
		if !ok || entry.ExpectedBlueprintID != o.SuggestedTemplateID || o.SuggestedTemplateID != row.Blueprint.BlueprintID {
			t.Errorf("%q: offer integration/blueprint mismatch", row.Shape)
		}
		if o.HomeProviderKey != "" {
			found := false
			for _, provider := range reviewedintegration.HomeProviders() {
				found = found || provider.Key == o.HomeProviderKey
			}
			if !found {
				t.Errorf("%q: unknown Home provider %q", row.Shape, o.HomeProviderKey)
			}
		}
		if o.Slug == "" || o.DisplayName == "" || o.OfferCopy.Headline == "" ||
			o.OfferCopy.Question == "" || o.OfferCopy.AcceptLabel == "" || o.OfferCopy.DeclineLabel == "" ||
			o.OfferCopy.AcceptedNote == "" || o.OfferCopy.ManualLabel == "" {
			t.Errorf("%q: incomplete offer copy or domain", row.Shape)
		}
	}
}

func claimExtension(t *testing.T, seen map[string]folderdigest.Shape, ext string, shape folderdigest.Shape) {
	t.Helper()
	if previous, ok := seen[ext]; ok && previous != shape {
		t.Errorf("extension %q claimed by both %q and %q", ext, previous, shape)
	}
	seen[ext] = shape
}

func TestCapabilityRowsAreDetached(t *testing.T) {
	rows := folderdigest.AllCapabilities()
	rows[0].Markers[0].Name = "forged"
	rows[0].Tools[0].ToolID = "forged"
	rows[0].Offer.AppPatterns[0][0] = "forged"
	rows[0].Offer.IntegrationKey = "forged"
	row, ok := folderdigest.CapabilityForShape(folderdigest.ShapeAudio)
	if !ok || row.Markers[0].Name != "*.rpp" || row.Tools[0].ToolID != "reaper" ||
		row.Offer.AppPatterns[0][0] != "reaper" || row.Offer.IntegrationKey != "ori_reaper" {
		t.Fatalf("caller mutated host table: %+v", row)
	}
}
