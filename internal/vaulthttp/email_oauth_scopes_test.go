package vaulthttp

import (
	"slices"
	"testing"
)

func TestGmailScopesForStageDefaultsToLeastPrivilege(t *testing.T) {
	got := gmailScopesForStage("")
	if !slices.Contains(got, gmailScopeReadonly) {
		t.Fatalf("connect stage must request gmail.readonly, got %v", got)
	}
	if slices.Contains(got, gmailScopeSend) {
		t.Fatalf("connect stage must NOT request gmail.send (least privilege), got %v", got)
	}
	// The old full-mailbox scope must never be requested.
	if slices.Contains(got, "https://mail.google.com/") {
		t.Fatalf("full-mailbox scope must not be requested, got %v", got)
	}
}

func TestGmailScopesForSendStageAddsSendOnTopOfRead(t *testing.T) {
	got := gmailScopesForStage("send")
	if !slices.Contains(got, gmailScopeReadonly) || !slices.Contains(got, gmailScopeSend) {
		t.Fatalf("send stage must request read + send, got %v", got)
	}
	if slices.Contains(got, "https://mail.google.com/") {
		t.Fatalf("send stage must use gmail.send, not full mailbox, got %v", got)
	}
}
