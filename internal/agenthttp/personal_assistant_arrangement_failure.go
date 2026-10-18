package agenthttp

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// ArrangementFailure is a safe recovery hint, never a serialized provider,
// filesystem or database error. It grants no permission or automatic retry.
type ArrangementFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func arrangementFailure(err error, fallback string) *ArrangementFailure {
	code := fallback
	switch {
	case errors.Is(err, context.Canceled):
		code = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "timed_out"
	case errors.Is(err, workspacearrangement.ErrBusy):
		code = "busy"
	case errors.Is(err, workspacearrangement.ErrStale), errors.Is(err, ErrPersonalAssistantFolderConflict), errors.Is(err, errAssistantWorkspaceScopeChanged), errors.Is(err, ErrPersonalAssistantConversationOwnerChanged):
		code = "stale_context"
	case errors.Is(err, workspacearrangement.ErrLimit):
		code = "limit_reached"
		if fallback == "save_failed" {
			code = "storage_limit"
		}
	case errors.Is(err, workspacearrangement.ErrInvalid):
		if fallback == "planning_failed" {
			code = "invalid_reply"
		}
	}
	messages := map[string]string{
		"invalid_request":       "Use the current assistant conversation and an organizing goal. Send planning separately from other approvals or actions.",
		"invalid_goal":          "Use a short, single-line organizing goal (up to 2,000 UTF-8 bytes), without control or invisible formatting characters.",
		"sharing_required":      "Before planning, explicitly choose to share additional bounded metadata from this attachment. Ordinary discussion remains available.",
		"source_required":       "Attach a folder for this planning turn. A path or a saved answer is not a live attachment.",
		"historical_source":     "This dated attachment remains discussable, but planning needs a fresh explicit attachment selection.",
		"focused_source":        "This planning mode uses the full held attachment. Clear the discussion focus and review the metadata-sharing choice before planning.",
		"source_unavailable":    "The held metadata is unavailable or no longer authorized. Reopen the conversation and explicitly reattach if needed; no scan will run automatically.",
		"provider_unavailable":  "The selected model is unavailable. Check its configuration or use the existing rule-based/manual setup; Ori has not switched providers.",
		"unsupported_transport": "The selected transport cannot provide metadata-only planning. Choose a supported tool-capable API model yourself, or use the existing rule-based/manual setup. No CLI or native tools were enabled.",
		"storage_unavailable":   "Canonical arrangement storage is unavailable. Your input is kept; use ordinary discussion or the existing rule-based/manual setup.",
		"busy":                  "An attachment or planning turn is already running. Wait or cancel it before sending another plan.",
		"stale_context":         "The conversation, attachment, owner or model changed. Reopen the current context and request a new plan; the old result was not offered.",
		"cancelled":             "Planning was cancelled. Your input is kept. Reopen saved history before deciding whether to send again.",
		"timed_out":             "Planning reached its time limit. Your input is kept. Reopen saved history before retrying with a narrower goal, or use the existing rule-based/manual setup.",
		"limit_reached":         "Planning exceeded a bounded input, evidence or model budget. Narrow the goal or selection, or use the existing rule-based/manual setup.",
		"storage_limit":         "The draft or conversation exceeds its arrangement storage bound. Existing records were preserved; narrow the draft, use a new conversation with an explicit attachment, or use manual setup.",
		"invalid_reply":         "The model did not produce a valid bounded arrangement. No draft is offered; revise the goal or use the existing rule-based/manual setup.",
		"save_failed":           "Saving the arrangement could not be confirmed. Your input is kept; reopen canonical history before retrying. No unsaved draft is offered.",
		"planning_failed":       "The selected model could not finish this planning turn. Your input is kept; retry explicitly or use the existing rule-based/manual setup.",
	}
	message, ok := messages[code]
	if !ok {
		code, message = "planning_failed", messages["planning_failed"]
	}
	return &ArrangementFailure{Code: code, Message: message + " This planning request has not applied an arrangement or changed source files or workspaces."}
}
