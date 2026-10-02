package mailbox

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"
)

// These tests drive the real IMAP protocol against go-imap's in-memory server
// over TLS, so threading, flags, folders, and fetch encoding are exercised the
// way a mail provider would answer them.

const (
	testIMAPUser     = "username"
	testIMAPPassword = "password"
	testIMAPOwner    = "me@example.com"
)

type testIMAPServer struct {
	t        *testing.T
	backend  *memory.Backend
	host     string
	port     int
	clientTL *tls.Config
	wire     *lockedBuffer
}

// lockedBuffer collects the server's wire transcript, which is written from the
// connection goroutine and read by the test.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newTestIMAPServer(t *testing.T) *testIMAPServer {
	t.Helper()
	return newTestIMAPServerWith(t, nil)
}

// newTestIMAPServerWith lets a test put a wrapper between the protocol server
// and the in-memory store, e.g. to make a command slow.
func newTestIMAPServerWith(t *testing.T, wrap func(backend.Backend) backend.Backend) *testIMAPServer {
	t.Helper()
	serverTLS, clientTLS := testTLSConfigs(t)

	be := memory.New()
	var served backend.Backend = be
	if wrap != nil {
		served = wrap(be)
	}
	srv := server.New(served)
	srv.ErrorLog = log.New(io.Discard, "", 0)
	wire := &lockedBuffer{}
	srv.Debug = wire

	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	s := &testIMAPServer{
		t:        t,
		backend:  be,
		host:     "127.0.0.1",
		port:     listener.Addr().(*net.TCPAddr).Port,
		clientTL: clientTLS,
		wire:     wire,
	}
	// memory.New seeds INBOX with one sample message; start from an empty one.
	s.mailbox(imapInbox).Messages = nil
	return s
}

// mailbox returns a folder, creating it on first use.
func (s *testIMAPServer) mailbox(name string) *memory.Mailbox {
	s.t.Helper()
	user, err := s.backend.Login(nil, testIMAPUser, testIMAPPassword)
	if err != nil {
		s.t.Fatalf("login to backend: %v", err)
	}
	mbox, err := user.GetMailbox(name)
	if err != nil {
		if err := user.CreateMailbox(name); err != nil {
			s.t.Fatalf("create mailbox %s: %v", name, err)
		}
		mbox, err = user.GetMailbox(name)
		if err != nil {
			s.t.Fatalf("get mailbox %s: %v", name, err)
		}
	}
	return mbox.(*memory.Mailbox)
}

type testMail struct {
	from, to, subject, messageID, inReplyTo, references string
	date                                                time.Time
	flags                                               []string
	// headers are extra raw header lines, e.g. a Content-Type.
	headers []string
	body    string
}

func (s *testIMAPServer) deliver(folder string, m testMail) {
	s.t.Helper()
	if m.to == "" {
		m.to = testIMAPOwner
	}
	if m.date.IsZero() {
		m.date = time.Now().Add(-time.Hour)
	}
	var raw strings.Builder
	fmt.Fprintf(&raw, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\n", m.from, m.to, m.subject, m.date.Format(time.RFC1123Z))
	if m.messageID != "" {
		fmt.Fprintf(&raw, "Message-ID: <%s>\r\n", m.messageID)
	}
	if m.inReplyTo != "" {
		fmt.Fprintf(&raw, "In-Reply-To: <%s>\r\n", m.inReplyTo)
	}
	if m.references != "" {
		fmt.Fprintf(&raw, "References: %s\r\n", m.references)
	}
	for _, h := range m.headers {
		raw.WriteString(h + "\r\n")
	}
	if len(m.headers) == 0 {
		raw.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	}
	raw.WriteString("\r\n" + m.body)
	body := raw.String()
	if err := s.mailbox(folder).CreateMessage(m.flags, m.date, bytes.NewBufferString(body)); err != nil {
		s.t.Fatalf("deliver: %v", err)
	}
}

// clientCommands returns the commands the client sent, without their tags.
// Client lines start with a tag; server lines start with "*" or "+". Lines of
// message data inside a literal can look like either, so callers match on
// command names rather than trusting every line.
func (s *testIMAPServer) clientCommands() []string {
	var out []string
	for _, line := range strings.Split(s.wire.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "*" || fields[0] == "+" {
			continue
		}
		out = append(out, strings.Join(fields[1:], " "))
	}
	return out
}

func (s *testIMAPServer) provider(password string) *IMAPProvider {
	p := NewIMAPProvider(staticIMAPResolver{creds: IMAPCredentials{
		Host: s.host, Port: s.port, Username: testIMAPUser, Password: password,
	}})
	p.tlsConfig = s.clientTL
	return p
}

type staticIMAPResolver struct {
	creds IMAPCredentials
	err   error
}

func (r staticIMAPResolver) IMAPCredentials(context.Context, string) (IMAPCredentials, error) {
	return r.creds, r.err
}

func imapTestAccount() Account {
	return Account{ID: "acct-1", Provider: "imap_smtp", EmailAddress: testIMAPOwner}
}

func testTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "imap test"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	serverTLS := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	return serverTLS, &tls.Config{RootCAs: roots}
}

func threadSubjects(threads []Thread) []string {
	out := make([]string, 0, len(threads))
	for _, th := range threads {
		out = append(out, th.Subject)
	}
	return out
}

func TestIMAPSearchThreadsGroupsConversationsNewestFirst(t *testing.T) {
	s := newTestIMAPServer(t)
	now := time.Now()
	s.deliver(imapInbox, testMail{from: "Alice <alice@example.com>", subject: "Lunch", messageID: "a1@example.com", date: now.Add(-3 * time.Hour), body: "Lunch on Friday?"})
	s.deliver(imapInbox, testMail{from: "bob@example.com", subject: "Invoice", messageID: "b1@example.com", date: now.Add(-2 * time.Hour), body: "Attached."})
	s.deliver(imapInbox, testMail{
		from: "Alice <alice@example.com>", subject: "Re: Lunch", messageID: "a2@example.com",
		inReplyTo: "a1@example.com", references: "<a1@example.com>", date: now.Add(-time.Hour), body: "Or Saturday.",
	})

	page, err := s.provider(testIMAPPassword).SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if got := threadSubjects(page.Threads); len(got) != 2 || got[0] != "Lunch" || got[1] != "Invoice" {
		t.Fatalf("threads = %q, want the Lunch conversation first and Invoice second", got)
	}
	lunch := page.Threads[0]
	if len(lunch.Messages) != 2 {
		t.Fatalf("Lunch has %d messages, want the original and the reply", len(lunch.Messages))
	}
	if lunch.Messages[0].ID != "a1@example.com" || lunch.Messages[1].ID != "a2@example.com" {
		t.Fatalf("Lunch messages out of order: %q, %q", lunch.Messages[0].ID, lunch.Messages[1].ID)
	}
	if lunch.Participants[0].Name != "Alice" || lunch.Participants[0].Address != "alice@example.com" {
		t.Fatalf("participant = %+v", lunch.Participants[0])
	}
	if lunch.AccountID != "acct-1" || lunch.Messages[0].ThreadID != lunch.ID {
		t.Fatalf("thread identity not carried: %+v", lunch)
	}
}

func TestIMAPSearchThreadsWaitingOnUser(t *testing.T) {
	s := newTestIMAPServer(t)
	s.deliver(imapInbox, testMail{from: "ask@example.com", subject: "Needs you", messageID: "q1@example.com", body: "Can you confirm?"})
	s.deliver(imapInbox, testMail{from: "read@example.com", subject: "Already read", messageID: "q2@example.com", flags: []string{imap.SeenFlag}, body: "FYI"})
	s.deliver(imapInbox, testMail{from: "done@example.com", subject: "Already answered", messageID: "q3@example.com", flags: []string{imap.AnsweredFlag}, body: "Thanks?"})

	p := s.provider(testIMAPPassword)
	all, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	waiting := map[string]bool{}
	for _, th := range all.Threads {
		waiting[th.Subject] = th.WaitingOnUser
	}
	if !waiting["Needs you"] || waiting["Already read"] || waiting["Already answered"] {
		t.Fatalf("waiting-on-user = %v", waiting)
	}

	only, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{WaitingOnUserOnly: true})
	if err != nil {
		t.Fatalf("SearchThreads waiting only: %v", err)
	}
	if got := threadSubjects(only.Threads); len(got) != 1 || got[0] != "Needs you" {
		t.Fatalf("waiting-only threads = %q", got)
	}
}

func TestIMAPSearchThreadsPagesWithoutOverlap(t *testing.T) {
	s := newTestIMAPServer(t)
	now := time.Now()
	for i, subject := range []string{"One", "Two", "Three"} {
		s.deliver(imapInbox, testMail{
			from: "sender@example.com", subject: subject, messageID: fmt.Sprintf("p%d@example.com", i),
			date: now.Add(time.Duration(i-5) * time.Hour), body: subject,
		})
	}

	p := s.provider(testIMAPPassword)
	var seen []string
	token := ""
	for range 5 {
		page, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{MaxResults: 2, PageToken: token})
		if err != nil {
			t.Fatalf("SearchThreads: %v", err)
		}
		seen = append(seen, threadSubjects(page.Threads)...)
		token = page.NextPageToken
		if token == "" {
			break
		}
	}
	if strings.Join(seen, ",") != "Three,Two,One" {
		t.Fatalf("paged threads = %q, want each thread once, newest first", seen)
	}
}

func TestIMAPSearchThreadsExcludesMailOutsideLookback(t *testing.T) {
	s := newTestIMAPServer(t)
	s.deliver(imapInbox, testMail{from: "old@example.com", subject: "Old", messageID: "old@example.com", date: time.Now().AddDate(0, 0, -20), body: "old"})
	s.deliver(imapInbox, testMail{from: "new@example.com", subject: "New", messageID: "new@example.com", body: "new"})

	page, err := s.provider(testIMAPPassword).SearchThreads(context.Background(), imapTestAccount(), Query{LookbackDays: 7})
	if err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if got := threadSubjects(page.Threads); len(got) != 1 || got[0] != "New" {
		t.Fatalf("threads = %q, want only mail inside the window", got)
	}
}

func TestIMAPGetThreadReadsTheUsersRepliesFromSent(t *testing.T) {
	s := newTestIMAPServer(t)
	now := time.Now()
	s.deliver(imapInbox, testMail{from: "carol@example.com", subject: "Contract", messageID: "c1@example.com", date: now.Add(-3 * time.Hour), body: "Can you sign by Monday?"})
	s.deliver("Sent", testMail{
		from: testIMAPOwner, to: "carol@example.com", subject: "Re: Contract", messageID: "c2@example.com",
		inReplyTo: "c1@example.com", references: "<c1@example.com>", date: now.Add(-2 * time.Hour), body: "Yes, signing today.",
	})
	// Unrelated mail in Sent must not join the thread.
	s.deliver("Sent", testMail{from: testIMAPOwner, to: "dan@example.com", subject: "Other", messageID: "x1@example.com", body: "hi"})

	p := s.provider(testIMAPPassword)
	page, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil || len(page.Threads) != 1 {
		t.Fatalf("SearchThreads = %d threads, %v", len(page.Threads), err)
	}
	thread, err := p.GetThread(context.Background(), imapTestAccount(), page.Threads[0].ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(thread.Messages) != 2 {
		t.Fatalf("thread has %d messages, want Carol's message and the user's reply", len(thread.Messages))
	}
	reply := thread.Messages[1]
	if !reply.FromUser || reply.Snippet != "Yes, signing today." {
		t.Fatalf("reply = %+v", reply)
	}
	if thread.WaitingOnUser {
		t.Fatal("the user replied last, so the thread is not waiting on them")
	}
}

func TestIMAPGetThreadDecodesBodies(t *testing.T) {
	s := newTestIMAPServer(t)
	now := time.Now()
	s.deliver(imapInbox, testMail{
		from: "eve@example.com", subject: "=?ISO-8859-1?Q?Caf=E9?=", messageID: "e1@example.com", date: now.Add(-2 * time.Hour),
		headers: []string{"Content-Type: text/plain; charset=iso-8859-1", "Content-Transfer-Encoding: quoted-printable"},
		body:    "Le caf=E9 est pr=EAt.\r\n\r\nOn Mon, someone wrote:\r\n> quoted history\r\n",
	})
	s.deliver(imapInbox, testMail{
		from: "eve@example.com", subject: "Re: Café", messageID: "e2@example.com", references: "<e1@example.com>", date: now.Add(-time.Hour),
		headers: []string{"Content-Type: text/html; charset=utf-8", "Content-Transfer-Encoding: base64"},
		body:    "PHA+SGVsbG8gPGI+dGhlcmU8L2I+PC9wPjxzY3JpcHQ+c3RlYWwoKTwvc2NyaXB0Pg==\r\n",
	})

	p := s.provider(testIMAPPassword)
	page, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil || len(page.Threads) != 1 {
		t.Fatalf("SearchThreads = %d threads, %v", len(page.Threads), err)
	}
	if page.Threads[0].Subject != "Café" {
		t.Fatalf("subject = %q, want the decoded Latin-1 subject", page.Threads[0].Subject)
	}
	thread, err := p.GetThread(context.Background(), imapTestAccount(), page.Threads[0].ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if got := thread.Messages[0].Snippet; got != "Le café est prêt." {
		t.Fatalf("quoted-printable body = %q", got)
	}
	if got := thread.Messages[1].Snippet; got != "Hello there" {
		t.Fatalf("base64 HTML body = %q, want tags and scripts stripped", got)
	}
}

func TestIMAPGetThreadNeverReadsAttachments(t *testing.T) {
	s := newTestIMAPServer(t)
	s.deliver(imapInbox, testMail{
		from: "frank@example.com", subject: "Report", messageID: "f1@example.com",
		headers: []string{`Content-Type: multipart/mixed; boundary="b1"`},
		body: "--b1\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\nContent-Disposition: attachment; filename=\"secrets.txt\"\r\n\r\nATTACHMENT-CONTENT\r\n" +
			"--b1\r\n" +
			"Content-Type: text/html; charset=utf-8\r\n\r\n<p>See the report.</p>\r\n" +
			"--b1--\r\n",
	})

	p := s.provider(testIMAPPassword)
	page, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil || len(page.Threads) != 1 {
		t.Fatalf("SearchThreads = %d threads, %v", len(page.Threads), err)
	}
	thread, err := p.GetThread(context.Background(), imapTestAccount(), page.Threads[0].ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if got := thread.Messages[0].Snippet; got != "See the report." {
		t.Fatalf("body = %q, want the message text and never the attachment", got)
	}
	if strings.Contains(s.wire.String(), "ATTACHMENT-CONTENT") {
		t.Fatal("the attachment was transferred from the server")
	}
}

func TestIMAPGetThreadWithoutMessageID(t *testing.T) {
	s := newTestIMAPServer(t)
	s.deliver(imapInbox, testMail{from: "gina@example.com", subject: "No id", body: "Plain note."})

	p := s.provider(testIMAPPassword)
	page, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil || len(page.Threads) != 1 {
		t.Fatalf("SearchThreads = %d threads, %v", len(page.Threads), err)
	}
	if !strings.HasPrefix(page.Threads[0].ID, imapThreadByUID) {
		t.Fatalf("thread id = %q, want a UID-addressed thread", page.Threads[0].ID)
	}
	thread, err := p.GetThread(context.Background(), imapTestAccount(), page.Threads[0].ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(thread.Messages) != 1 || thread.Messages[0].Snippet != "Plain note." {
		t.Fatalf("thread = %+v", thread)
	}
}

func TestIMAPGetThreadUnknownIDIsNotFound(t *testing.T) {
	s := newTestIMAPServer(t)
	s.deliver(imapInbox, testMail{from: "h@example.com", subject: "Hi", messageID: "h1@example.com", body: "hi"})

	p := s.provider(testIMAPPassword)
	for _, id := range []string{"", "nonsense", imapThreadByUID + "1.999", imapThreadByMessageID + "bm9wZUBleGFtcGxlLmNvbQ"} {
		if _, err := p.GetThread(context.Background(), imapTestAccount(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetThread(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

// The reader must leave the mailbox exactly as it found it. The transcript is
// the proof: every folder is opened with EXAMINE (read-only), every body is
// fetched with BODY.PEEK (does not mark read), and no command that changes a
// mailbox is ever sent.
func TestIMAPReadsNeverModifyTheMailbox(t *testing.T) {
	s := newTestIMAPServer(t)
	s.deliver(imapInbox, testMail{from: "i@example.com", subject: "Read me", messageID: "i1@example.com", body: "unread body"})
	s.deliver("Sent", testMail{from: testIMAPOwner, subject: "Re: Read me", messageID: "i2@example.com", references: "<i1@example.com>", body: "reply"})

	p := s.provider(testIMAPPassword)
	page, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil || len(page.Threads) != 1 {
		t.Fatalf("SearchThreads = %d threads, %v", len(page.Threads), err)
	}
	if _, err := p.GetThread(context.Background(), imapTestAccount(), page.Threads[0].ID); err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if !strings.Contains(s.wire.String(), "EXAMINE") {
		t.Fatal("expected folders to be opened with EXAMINE")
	}
	forbidden := regexp.MustCompile(`(?i)\b(SELECT|STORE|COPY|MOVE|EXPUNGE|APPEND|DELETE|RENAME|CREATE)\b`)
	for _, command := range s.clientCommands() {
		fields := strings.Fields(command)
		if forbidden.MatchString(fields[0]) || (strings.EqualFold(fields[0], "UID") && len(fields) > 1 && forbidden.MatchString(fields[1])) {
			t.Fatalf("the reader sent a mailbox-changing command: %q", command)
		}
		if strings.Contains(strings.ToUpper(command), "BODY[") {
			t.Fatalf("the reader fetched a body without PEEK, which marks mail read: %q", command)
		}
	}
	for _, msg := range s.mailbox(imapInbox).Messages {
		for _, flag := range msg.Flags {
			if flag == imap.SeenFlag {
				t.Fatal("an inbox message was marked read")
			}
		}
	}
}

// Opening a conversation must not ask the server to search reply headers.
// Gmail answers that by reading the whole mailbox: on a real inbox it ran past
// the command time limit and opening any conversation failed.
func TestIMAPGetThreadNeverAsksTheServerToSearchHeaders(t *testing.T) {
	s := newTestIMAPServer(t)
	now := time.Now()
	s.deliver(imapInbox, testMail{from: "l@example.com", subject: "Plans", messageID: "l1@example.com", date: now.Add(-2 * time.Hour), body: "plans"})
	s.deliver(imapInbox, testMail{from: "l@example.com", subject: "Re: Plans", messageID: "l2@example.com", references: "<l1@example.com>", date: now.Add(-time.Hour), body: "more"})
	s.deliver("Sent", testMail{from: testIMAPOwner, subject: "Re: Plans", messageID: "l3@example.com", references: "<l1@example.com> <l2@example.com>", body: "ok"})

	p := s.provider(testIMAPPassword)
	page, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil || len(page.Threads) != 1 {
		t.Fatalf("SearchThreads = %d threads, %v", len(page.Threads), err)
	}
	thread, err := p.GetThread(context.Background(), imapTestAccount(), page.Threads[0].ID)
	if err != nil || len(thread.Messages) != 3 {
		t.Fatalf("GetThread = %d messages, %v; want both inbox messages and the sent reply", len(thread.Messages), err)
	}
	headerSearch := regexp.MustCompile(`(?i)\bSEARCH\b.*\bHEADER\b`)
	for _, command := range s.clientCommands() {
		if headerSearch.MatchString(command) {
			t.Fatalf("the reader asked the server to search headers: %q", command)
		}
	}
}

// stallingBackend makes every search take delay, like a server working through
// a large mailbox.
type stallingBackend struct {
	backend.Backend
	delay time.Duration
}

func (b stallingBackend) Login(info *imap.ConnInfo, username, password string) (backend.User, error) {
	user, err := b.Backend.Login(info, username, password)
	if err != nil {
		return nil, err
	}
	return stallingUser{User: user, delay: b.delay}, nil
}

type stallingUser struct {
	backend.User
	delay time.Duration
}

func (u stallingUser) GetMailbox(name string) (backend.Mailbox, error) {
	mbox, err := u.User.GetMailbox(name)
	if err != nil {
		return nil, err
	}
	return stallingMailbox{Mailbox: mbox, delay: u.delay}, nil
}

type stallingMailbox struct {
	backend.Mailbox
	delay time.Duration
}

func (m stallingMailbox) SearchMessages(uid bool, criteria *imap.SearchCriteria) ([]uint32, error) {
	time.Sleep(m.delay)
	return m.Mailbox.SearchMessages(uid, criteria)
}

func TestIMAPStalledCommandIsATimeout(t *testing.T) {
	s := newTestIMAPServerWith(t, func(be backend.Backend) backend.Backend {
		return stallingBackend{Backend: be, delay: 2 * time.Second}
	})
	s.deliver(imapInbox, testMail{from: "m@example.com", subject: "Slow", messageID: "m1@example.com", body: "slow"})

	p := s.provider(testIMAPPassword)
	p.commandTimeout = 200 * time.Millisecond
	started := time.Now()
	_, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout: a stalled command is worth retrying", err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("took %s, want the command time limit to end the read", elapsed)
	}
}

func TestIMAPWrongPasswordIsExpired(t *testing.T) {
	s := newTestIMAPServer(t)
	_, err := s.provider("wrong-password").SearchThreads(context.Background(), imapTestAccount(), Query{})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired so the user is asked to reconnect", err)
	}
}

func TestIMAPUnreachableServerIsTypedAndSecretFree(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	const secret = "super-secret-app-password"
	p := NewIMAPProvider(staticIMAPResolver{creds: IMAPCredentials{Host: "127.0.0.1", Port: port, Username: testIMAPUser, Password: secret}})
	_, err = p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if !errors.Is(err, ErrProvider) && !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want a typed mailbox error", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaks connection detail: %q", err)
	}
}

func TestIMAPUntrustedCertificateIsRefused(t *testing.T) {
	s := newTestIMAPServer(t)
	p := s.provider(testIMAPPassword)
	p.tlsConfig = nil // the system roots do not trust the test server
	_, err := p.SearchThreads(context.Background(), imapTestAccount(), Query{})
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("err = %v, want the connection refused before login", err)
	}
	if strings.Contains(s.wire.String(), testIMAPPassword) {
		t.Fatal("the password was sent to a server whose certificate was not trusted")
	}
}

func TestIMAPMissingCredentialsIsDisconnected(t *testing.T) {
	for name, resolver := range map[string]IMAPCredentialResolver{
		"resolver error": staticIMAPResolver{err: ErrDisconnected},
		"no password":    staticIMAPResolver{creds: IMAPCredentials{Host: "imap.example.com", Username: "u"}},
		"no host":        staticIMAPResolver{creds: IMAPCredentials{Username: "u", Password: "p"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewIMAPProvider(resolver).SearchThreads(context.Background(), imapTestAccount(), Query{})
			if !errors.Is(err, ErrDisconnected) {
				t.Fatalf("err = %v, want ErrDisconnected", err)
			}
		})
	}
	if _, err := (*IMAPProvider)(nil).GetThread(context.Background(), imapTestAccount(), "u.1.1"); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil provider err = %v", err)
	}
}

func TestParseIMAPThreadIDRejectsMalformed(t *testing.T) {
	for _, id := range []string{
		"", "x", "m.", "m.!!!", "u.", "u.1", "u.1.0", "u.a.1", "u.1.b",
		// A root that would break out of a header search string.
		imapThreadByMessageID + "PGE-",           // "<a>"
		imapThreadByMessageID + "YSBi",           // "a b"
		imapThreadByMessageID + "YQ0KU1RPUkUgMQ", // "a\r\nSTORE 1"
	} {
		if _, err := parseIMAPThreadID(id); err == nil {
			t.Errorf("parseIMAPThreadID(%q) accepted a malformed id", id)
		}
	}
	ref, err := parseIMAPThreadID(imapMeta{messageID: "ok@example.com"}.threadID())
	if err != nil || ref.rootID != "ok@example.com" {
		t.Fatalf("round trip = %+v, %v", ref, err)
	}
}

func TestMessageIDsExtractsInOrder(t *testing.T) {
	got := messageIDs("<a@x>\r\n <b@x> junk <c d> <e@x>")
	if strings.Join(got, ",") != "a@x,b@x,e@x" {
		t.Fatalf("messageIDs = %q", got)
	}
	for value, want := range map[string]string{"<a@x>": "a@x", " bare@x ": "bare@x", "no-at-sign": "", "two words@x": ""} {
		if got := firstMessageID(value); got != want {
			t.Errorf("firstMessageID(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestIMAPThreadsJoinARootWithABareMessageID(t *testing.T) {
	s := newTestIMAPServer(t)
	now := time.Now()
	s.deliver(imapInbox, testMail{
		from: "k@example.com", subject: "Bare", date: now.Add(-2 * time.Hour), body: "first",
		headers: []string{"Message-ID: bare1@example.com", "Content-Type: text/plain"},
	})
	s.deliver(imapInbox, testMail{from: "k@example.com", subject: "Re: Bare", messageID: "bare2@example.com", references: "<bare1@example.com>", body: "second"})

	page, err := s.provider(testIMAPPassword).SearchThreads(context.Background(), imapTestAccount(), Query{})
	if err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if len(page.Threads) != 1 || len(page.Threads[0].Messages) != 2 {
		t.Fatalf("threads = %q, want one conversation of two messages", threadSubjects(page.Threads))
	}
}

func TestIsConventionalSentName(t *testing.T) {
	for name, want := range map[string]bool{
		"Sent": true, "Sent Messages": true, "[Gmail]/Sent Mail": true, "INBOX.Sent": true, "Sent Items": true,
		"Sentimental": false, "INBOX": false, "Drafts": false,
	} {
		delimiter := "/"
		if strings.HasPrefix(name, "INBOX.") {
			delimiter = "."
		}
		if got := isConventionalSentName(name, delimiter); got != want {
			t.Errorf("isConventionalSentName(%q) = %v, want %v", name, got, want)
		}
	}
}
