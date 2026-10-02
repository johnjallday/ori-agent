//go:build live

package mailbox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestIMAPLive reads a real mailbox through the IMAP reader. It only runs with
// the live build tag and credentials in the environment, so CI never touches a
// real account. Run it from your own terminal, where the password stays out of
// shell history:
//
//	read -rs ORI_IMAP_LIVE_PASSWORD && export ORI_IMAP_LIVE_PASSWORD
//	ORI_IMAP_LIVE_USER=you@example.com go test -tags live -run TestIMAPLive -v ./internal/mailbox/
//	unset ORI_IMAP_LIVE_PASSWORD
//
// ORI_IMAP_LIVE_HOST defaults to Gmail's IMAP server; set it for other
// providers. It lists subjects and senders only: no message text is printed.
func TestIMAPLive(t *testing.T) {
	user := strings.TrimSpace(os.Getenv("ORI_IMAP_LIVE_USER"))
	password := os.Getenv("ORI_IMAP_LIVE_PASSWORD")
	if user == "" || password == "" {
		t.Skip("set ORI_IMAP_LIVE_USER and ORI_IMAP_LIVE_PASSWORD to read a real mailbox")
	}
	host := strings.TrimSpace(os.Getenv("ORI_IMAP_LIVE_HOST"))
	if host == "" {
		host = "imap.gmail.com"
	}

	p := NewIMAPProvider(staticIMAPResolver{creds: IMAPCredentials{Host: host, Port: imapImplicitTLSPort, Username: user, Password: password}})
	account := Account{ID: "live", Provider: "imap_smtp", EmailAddress: user}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	started := time.Now()
	page, err := p.SearchThreads(ctx, account, Query{MaxResults: 10})
	if err != nil {
		t.Fatalf("SearchThreads: %v (ErrExpired means the server refused the login)", err)
	}
	t.Logf("listed %d conversations in %s", len(page.Threads), time.Since(started).Round(time.Millisecond))
	waiting := 0
	for _, th := range page.Threads {
		from := ""
		if len(th.Participants) > 0 {
			from = th.Participants[0].Address
		}
		mark := " "
		if th.WaitingOnUser {
			mark = "*"
			waiting++
		}
		t.Logf("%s %s  %-32.32s  %.60s", mark, th.LastMessageAt.Local().Format("Jan 02 15:04"), from, th.Subject)
	}
	t.Logf("* = waiting on you (%d of %d)", waiting, len(page.Threads))
	if len(page.Threads) == 0 {
		return
	}

	started = time.Now()
	thread, err := p.GetThread(ctx, account, page.Threads[0].ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	mine, withText := 0, 0
	for _, m := range thread.Messages {
		if m.FromUser {
			mine++
		}
		if m.Snippet != "" {
			withText++
		}
	}
	t.Logf("read the newest conversation in %s: %d messages, %d from you, %d with readable text",
		time.Since(started).Round(time.Millisecond), len(thread.Messages), mine, withText)
}
