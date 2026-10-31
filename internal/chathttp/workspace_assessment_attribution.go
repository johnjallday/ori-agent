package chathttp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// workspaceAssessmentSourceNote formats only typed metadata from a successful
// native read, not the assessment's prose or the model's reply. Keep this on the
// tool result so a later workspace change cannot silently redate the evidence
// the model actually saw. Nothing is reread or started during response rendering.
func workspaceAssessmentSourceNote(tool toolapi.Tool, result string, success bool) string {
	if _, native := tool.(*workspaceAssessmentTool); !native || !success {
		return ""
	}
	var view workspaceAssessmentView
	if json.Unmarshal([]byte(result), &view) != nil || view.ReadAt.IsZero() {
		return ""
	}
	return assessmentViewSourceNote(view)
}

// Also used by the host-written first conversation, from the same canonical
// assessment projection. No model-authored metadata enters this formatter.
func assessmentViewSourceNote(view workspaceAssessmentView) string {
	parts := []string{}
	switch {
	case view.Status == "available" && view.Assessment != nil:
		a := view.Assessment
		switch {
		case !a.FinishedAt.IsZero():
			parts = append(parts, "saved assessment completed "+assessmentSourceTime(a.FinishedAt))
		case !a.StartedAt.IsZero():
			parts = append(parts, "saved assessment started "+assessmentSourceTime(a.StartedAt)+" (completion date unavailable)")
		default:
			parts = append(parts, "saved assessment date unavailable")
		}
		switch a.GoalMatch {
		case "current":
			parts = append(parts, "same goal text")
		case "changed":
			parts = append(parts, "goal has since changed")
		default:
			parts = append(parts, "goal match unverified")
		}
	case view.Status == "missing" && view.Assessment == nil:
		parts = append(parts, "no saved assessment available")
	default:
		return ""
	}
	parts = append(parts, "stored task snapshot read "+assessmentSourceTime(view.ReadAt))
	if view.LatestRun != nil && !view.LatestRun.Succeeded {
		parts = append(parts, "latest recorded review failed")
	}
	return "Source: " + strings.Join(parts, "; ") + ". This read did not run a review."
}

func assessmentSourceTime(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04 UTC")
}
