package agenthttp

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

func TestHireRequestHintAndChipBrief(t *testing.T) {
	for _, prompt := range []string{"hire someone to compare the three CRMs", "Have someone look into our churn", "get an agent to draft the launch post", "Please delegate the vendor comparison", "Yes, hire a researcher for the CRM question"} {
		if !isHireRequestHint(prompt) || !isAssistantWorkspaceReviewRequest(prompt) {
			t.Fatalf("hire phrasing missed: %q", prompt)
		}
	}
	for _, prompt := range []string{"delegate", "what does delegate mean?", "should I hire someone?", "yes", "the hiring freeze ends in May", "Don't hire anyone yet"} {
		if isHireRequestHint(prompt) {
			t.Fatalf("not a hire request: %q", prompt)
		}
	}
	for prompt, want := range map[string]string{
		"hire someone to compare the three CRMs":          "Compare the three CRMs",
		"Please have someone look into our churn":         "Look into our churn",
		"Could you get an agent to draft the launch post": "Draft the launch post",
		"prepare a workspace review for the March launch": "The March launch",
		"delegate   the vendor\n comparison":              "The vendor comparison",
		"hire someone to":                                 "",
	} {
		if got := hireChipBrief(prompt); got != want {
			t.Fatalf("brief for %q = %q, want %q", prompt, got, want)
		}
	}
	if brief := hireChipBrief("hire someone to " + strings.Repeat("word ", 120)); len([]rune(brief)) > 300 || !strings.HasPrefix(brief, "Word word") {
		t.Fatalf("brief not bounded: %q", brief)
	}
	if href := hireChipHref("Compare the three CRMs & pick one"); href != "/agents?brief=Compare+the+three+CRMs+%26+pick+one&hire=1" {
		t.Fatalf("href %q", href)
	}
}

func TestHireChipIsOfferedOnlyWithoutToolsAndOnlyForAHireRequest(t *testing.T) {
	// A provider that cannot call tools: the host's chip stands in for the
	// model's offer, and the rules tell the model to claim nothing.
	f, _, _, proposals := newHireProposalFixture(t)
	snapshot := &scriptedProvider{answers: []string{"I can help with that; the drawer offers a Hire an agent for this chip under this reply.", "A CRM keeps customer records.", "Here is the comparison outline."}}
	f.handler.LLMFactory.Register("fake", snapshot)
	response := askHire(f, "hire someone to compare the three CRMs we're evaluating")
	if response.Confirmation != nil || len(proposals.order) != 0 {
		t.Fatalf("a card without tools: %+v", response)
	}
	if len(response.Actions) != 1 || response.Actions[0].Type != HomeActionNavigate || response.Actions[0].Label != "Hire an agent for this" ||
		!strings.HasPrefix(response.Actions[0].Href, "/agents?") || !strings.Contains(response.Actions[0].Href, "hire=1") || !strings.Contains(response.Actions[0].Href, "Compare+the+three+CRMs") {
		t.Fatalf("chip: %+v", response.Actions)
	}
	rules := systemText(snapshot.requests[0])
	for _, rule := range []string{"Hire an agent for this chip", "Do not claim that a card, a hire, an agent or a workspace exists"} {
		if !strings.Contains(rules, rule) {
			t.Fatalf("manual rules lack %q", rule)
		}
	}
	// A question gets no chip, and so does a request that is work but asks for
	// no hire in so many words: without tools the host does not guess.
	for _, prompt := range []string{"what is a CRM?", "help me plan the product launch for March"} {
		response = askHire(f, prompt)
		if response.Confirmation != nil || len(response.Actions) != 0 {
			t.Fatalf("%q got a chip or a card: %+v", prompt, response.Actions)
		}
	}

	// With tools the model proposes; the host adds no chip beside the card or
	// beside a plain answer.
	g, _, _, _ := newHireProposalFixture(t)
	g.provider.script = []llm.ChatResponse{{Content: "Happy to; which three CRMs are they?"}}
	withTools := askHire(g, "hire someone to compare the three CRMs we're evaluating")
	if len(withTools.Actions) != 0 {
		t.Fatalf("a chip beside the tool: %+v", withTools.Actions)
	}
}
