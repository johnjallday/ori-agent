package runtimecapability

import "testing"

func TestSanitizeAction_PreservesBoundedResponseOnlyExactDisclosure(t *testing.T) {
	action := sanitizeAction(&Action{
		Token: "repair", Code: "repair_provider", Label: "Stage trusted runner",
		Disclosure: []ActionDisclosure{
			{Label: "Trusted destination", Value: "/Users/example/Library/Application Support/REAPER/Scripts/Ori/ori-reaper-runner.lua"},
			{Label: "Manual registration", Value: "Load the staged script from REAPER's Action List and run it once."},
		},
	})
	if action == nil || len(action.Disclosure) != 2 || action.Disclosure[0].Value == "" {
		t.Fatalf("sanitized action = %#v", action)
	}
	if sanitizeAction(&Action{
		Token: "repair", Code: "repair_provider", Label: "Repair",
		Disclosure: []ActionDisclosure{{Label: "Bad", Value: "line one\nline two"}},
	}) != nil {
		t.Fatal("multiline action disclosure was accepted")
	}
}
