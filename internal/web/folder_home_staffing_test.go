package web

import (
	"strings"
	"testing"
)

func TestRenderedFolderHomeStaffingPrecedesActionsAndCollapsedDetails(t *testing.T) {
	r := NewTemplateRenderer()
	if err := r.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := r.RenderTemplate("index", GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	staffing := strings.Index(html, `id="personalAssistantFolderHomeStaffing"`)
	actions := strings.Index(html, `id="personalAssistantFolderOfferActions"`)
	details := strings.Index(html, `id="personalAssistantFolderDetails"`)
	if staffing < 0 || actions < staffing || details < staffing || strings.Count(html, `id="personalAssistantFolderHomeStaffing"`) != 1 {
		t.Fatal("required Home choices must be a single visible review section before actions, not inside collapsed details")
	}
	roster := strings.Index(html, `/js/modules/workspace-role-roster.js`)
	control := strings.Index(html, `/js/modules/folder-home-staffing.js`)
	if roster < 0 || control <= roster {
		t.Fatal("shared role renderer must load before folder Home controls")
	}
}
