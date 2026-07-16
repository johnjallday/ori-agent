package chathttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mailbox"
)

type fakeMailProvider struct {
	page mailbox.ThreadPage
	err  error
}

func (f fakeMailProvider) SearchThreads(ctx context.Context, a mailbox.Account, q mailbox.Query) (mailbox.ThreadPage, error) {
	return f.page, f.err
}
func (f fakeMailProvider) GetThread(ctx context.Context, a mailbox.Account, id string) (mailbox.Thread, error) {
	if f.err != nil {
		return mailbox.Thread{}, f.err
	}
	return mailbox.Thread{ID: id, Subject: "Re: hi", Messages: []mailbox.Message{{From: mailbox.Participant{Address: "dana@x.com"}, Snippet: "body"}}}, nil
}

type fakeMailAccess struct {
	provider  mailbox.MailboxProvider
	canAccess bool
	acctErr   error
}

func (f fakeMailAccess) CanAccess(workspaceID, agentName string) bool { return f.canAccess }
func (f fakeMailAccess) AuthorizedAccount(ctx context.Context, workspaceID, agentName string) (mailbox.Account, error) {
	if f.acctErr != nil {
		return mailbox.Account{}, f.acctErr
	}
	return mailbox.Account{ID: "acct-1", Provider: "gmail", EmailAddress: "me@x.com"}, nil
}
func (f fakeMailAccess) Provider() mailbox.MailboxProvider { return f.provider }

func providerWithMail(access MailboxAccess, agent string) *WorkspaceToolProvider {
	p := NewWorkspaceToolProvider(nil, nil, "hq-1")
	p.SetExecutingAgent(agent)
	p.SetMailboxAccess(access)
	return p
}

type fakeDrafter struct{ called bool }

func (f *fakeDrafter) DraftReply(ctx context.Context, userID, threadID, body string) (*mailbox.ReplyProposal, error) {
	f.called = true
	return &mailbox.ReplyProposal{ID: "p1", Payload: mailbox.ReplyPayload{To: []string{"dana@x.com"}, Subject: "Re: hi", Body: body}}, nil
}

func TestMailDraftReplyToolCreatesLocalProposal(t *testing.T) {
	prov := fakeMailProvider{}
	p := providerWithMail(fakeMailAccess{provider: prov, canAccess: true}, "Inbox")
	drafter := &fakeDrafter{}
	p.SetMailDrafter(drafter)

	if !toolNames(p)["mail_draft_reply"] {
		t.Fatal("authorized agent with a drafter should see mail_draft_reply")
	}
	tool := findTool(t, p, "mail_draft_reply")
	out, err := tool.Call(context.Background(), `{"thread_id":"t1","body":"Sounds good."}`)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if !drafter.called || !strings.Contains(out, "proposal_id") || !strings.Contains(out, "confirm") {
		t.Fatalf("draft tool should create a proposal and note confirmation, got %s", out)
	}
}

func TestMailDraftReplyHiddenWithoutDrafter(t *testing.T) {
	prov := fakeMailProvider{}
	p := providerWithMail(fakeMailAccess{provider: prov, canAccess: true}, "Inbox")
	if toolNames(p)["mail_draft_reply"] {
		t.Fatal("mail_draft_reply must not appear without a drafter wired")
	}
}

func TestMailToolsExposedOnlyWhenAuthorized(t *testing.T) {
	prov := fakeMailProvider{}
	// Authorized: tools present.
	p := providerWithMail(fakeMailAccess{provider: prov, canAccess: true}, "Inbox")
	names := toolNames(p)
	if !names["mail_search_threads"] || !names["mail_get_thread"] {
		t.Fatalf("authorized agent should see mail tools, got %v", names)
	}

	// Not authorized (CanAccess false): tools hidden.
	p2 := providerWithMail(fakeMailAccess{provider: prov, canAccess: false}, "Journal")
	if toolNames(p2)["mail_search_threads"] {
		t.Fatal("unauthorized agent must not see mail tools")
	}

	// No access wired at all: hidden.
	p3 := NewWorkspaceToolProvider(nil, nil, "hq-1")
	if toolNames(p3)["mail_search_threads"] {
		t.Fatal("mail tools must not appear without a mailbox access boundary")
	}
}

func TestMailSearchThreadsReturnsBoundedList(t *testing.T) {
	prov := fakeMailProvider{page: mailbox.ThreadPage{Threads: []mailbox.Thread{
		{ID: "t1", Subject: "Need review", WaitingOnUser: true, Participants: []mailbox.Participant{{Name: "Dana", Address: "dana@x.com"}}},
	}}}
	p := providerWithMail(fakeMailAccess{provider: prov, canAccess: true}, "Inbox")

	var tool = findTool(t, p, "mail_search_threads")
	out, err := tool.Call(context.Background(), `{"waiting_on_user_only":true}`)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(out, "Need review") || !strings.Contains(out, "waiting_on_user") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, "Dana") {
		t.Fatalf("expected participant label, got %s", out)
	}
}

func TestMailToolDeniedAccessGivesClearMessage(t *testing.T) {
	p := providerWithMail(fakeMailAccess{provider: fakeMailProvider{}, canAccess: true, acctErr: mailbox.ErrPermissionDenied}, "Inbox")
	tool := findTool(t, p, "mail_search_threads")
	_, err := tool.Call(context.Background(), `{}`)
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("expected an authorization error, got %v", err)
	}
}

func TestMailGetThreadRequiresThreadID(t *testing.T) {
	p := providerWithMail(fakeMailAccess{provider: fakeMailProvider{}, canAccess: true}, "Inbox")
	tool := findTool(t, p, "mail_get_thread")
	if _, err := tool.Call(context.Background(), `{}`); err == nil {
		t.Fatal("expected an error when thread_id is missing")
	}
	out, err := tool.Call(context.Background(), `{"thread_id":"t1"}`)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(out, "Re: hi") || !strings.Contains(out, "body") {
		t.Fatalf("unexpected thread output: %s", out)
	}
}

func findTool(t *testing.T, p *WorkspaceToolProvider, name string) interface {
	Call(context.Context, string) (string, error)
} {
	t.Helper()
	for _, tool := range p.Tools() {
		if tool.Definition().Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
