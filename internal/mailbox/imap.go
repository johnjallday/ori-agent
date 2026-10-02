package mailbox

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/charset"
)

// IMAP is the read path for mailboxes connected with a password or an app
// password — iCloud, Yahoo, Fastmail, a custom domain, or Gmail without Ori's
// Google sign-in. One credential grants the whole mailbox, so the limits here
// are Ori's own, and they are structural rather than advisory:
//
//   - every mailbox is opened with EXAMINE, which the server treats as
//     read-only, so no command on the session can change a flag or a message;
//   - bodies are fetched with BODY.PEEK, which never marks a message read;
//   - only INBOX and the Sent folder are opened, so spam, trash, and drafts are
//     out of reach rather than filtered out;
//   - every read is bounded (messages scanned, messages per thread, bytes per
//     body), and attachments are never downloaded.
const (
	imapInbox = "INBOX"
	// imapImplicitTLSPort is the default IMAPS port. imapStartTLSPort is the one
	// port where the session starts in clear text and must upgrade before login.
	imapImplicitTLSPort = 993
	imapStartTLSPort    = 143

	imapDialTimeout    = 10 * time.Second
	imapCommandTimeout = 20 * time.Second
	// imapSessionTimeout bounds one whole read when the caller set no deadline.
	imapSessionTimeout = 45 * time.Second

	// imapScanLimit is how many of the newest inbox messages inside the lookback
	// window are read to build the thread list. Older mail in a busier inbox is
	// simply not listed; nothing here ever reads an unbounded mailbox.
	imapScanLimit = 200
	// imapMaxThreadMessages bounds one thread, per folder.
	imapMaxThreadMessages = 25
	// imapMaxBodyBytes bounds the text fetched for one message before decoding.
	imapMaxBodyBytes = 64 * 1024
	// imapMaxHeaderBytes bounds the References header read for one message.
	imapMaxHeaderBytes = 16 * 1024
)

func init() {
	// Subjects and sender names arrive as RFC 2047 encoded words in whatever
	// charset the sender used. Without this the library decodes UTF-8 only.
	imap.CharsetReader = charset.Reader
}

// IMAPCredentials is what the adapter needs to open one mailbox. It exists only
// between the credential resolver and the IMAP session: it is never returned to
// a caller, logged, or placed in an error.
type IMAPCredentials struct {
	Host     string
	Port     int
	Username string
	Password string
}

// IMAPCredentialResolver resolves a mailbox account ID to its IMAP login. Like
// CredentialResolver it is the only boundary that touches stored secrets, and it
// answers with a typed mailbox error when the account has no usable login.
type IMAPCredentialResolver interface {
	IMAPCredentials(ctx context.Context, accountID string) (IMAPCredentials, error)
}

// IMAPProvider is the IMAP-backed MailboxProvider.
type IMAPProvider struct {
	resolver IMAPCredentialResolver
	// tlsConfig is nil in production, which means the system roots and the
	// account's own host name. Tests set it to trust a local server.
	tlsConfig *tls.Config
	now       func() time.Time
}

// NewIMAPProvider constructs the IMAP-backed MailboxProvider.
func NewIMAPProvider(resolver IMAPCredentialResolver) *IMAPProvider {
	return &IMAPProvider{resolver: resolver, now: time.Now}
}

var _ MailboxProvider = (*IMAPProvider)(nil)

// SearchThreads lists the inbox's recent conversations, newest first. Threads
// are rebuilt from the Message-ID / References headers because IMAP itself has
// no thread object.
func (p *IMAPProvider) SearchThreads(ctx context.Context, account Account, q Query) (ThreadPage, error) {
	q = q.Normalized()
	before, err := decodeIMAPPageToken(q.PageToken)
	if err != nil {
		return ThreadPage{}, ErrProvider
	}

	var metas []imapMeta
	err = p.withSession(ctx, account, func(c *client.Client) error {
		status, err := c.Select(imapInbox, true)
		if err != nil {
			return err
		}
		criteria := imap.NewSearchCriteria()
		criteria.Since = p.now().AddDate(0, 0, -q.LookbackDays)
		uids, err := c.UidSearch(criteria)
		if err != nil {
			return err
		}
		metas, err = fetchIMAPMeta(c, newestUIDs(uids, imapScanLimit), imapMailboxRef{name: imapInbox, uidValidity: status.UidValidity}, false)
		return err
	})
	if err != nil {
		return ThreadPage{}, err
	}

	var page ThreadPage
	var lastUID uint32
	for _, group := range groupIMAPThreads(metas) {
		if before != 0 && group.lastUID >= before {
			continue // already returned on an earlier page
		}
		thread := group.thread(account)
		if q.WaitingOnUserOnly && !thread.WaitingOnUser {
			continue
		}
		if len(page.Threads) == q.MaxResults {
			// More remain: the cursor is the last thread this page returned.
			page.NextPageToken = encodeIMAPPageToken(lastUID)
			break
		}
		page.Threads = append(page.Threads, thread)
		lastUID = group.lastUID
	}
	return page, nil
}

// GetThread returns one conversation with its bounded, sanitized message text.
// The user's own replies are read from the Sent folder when the server has one,
// so a thread reads as the exchange it was rather than one side of it.
func (p *IMAPProvider) GetThread(ctx context.Context, account Account, threadID string) (Thread, error) {
	ref, err := parseIMAPThreadID(threadID)
	if err != nil {
		return Thread{}, ErrNotFound
	}

	var metas []imapMeta
	err = p.withSession(ctx, account, func(c *client.Client) error {
		status, err := c.Select(imapInbox, true)
		if err != nil {
			return err
		}
		inbox := imapMailboxRef{name: imapInbox, uidValidity: status.UidValidity}
		uids, err := threadUIDs(c, ref, inbox)
		if err != nil {
			return err
		}
		metas, err = fetchIMAPMeta(c, newestUIDs(uids, imapMaxThreadMessages), inbox, true)
		if err != nil {
			return err
		}
		if ref.rootID == "" {
			return nil // a message with no Message-ID cannot have replies
		}
		// The Sent folder is a courtesy: a server without one, or one that
		// refuses it, still yields the inbox side of the thread.
		if sent := readSentSide(c, ref); len(sent) > 0 {
			metas = append(metas, sent...)
		}
		return nil
	})
	if err != nil {
		return Thread{}, err
	}

	for _, group := range groupIMAPThreads(metas) {
		if group.id == threadID {
			return group.thread(account), nil
		}
	}
	return Thread{}, ErrNotFound
}

// readSentSide returns the thread's messages from the Sent folder, or nil when
// the folder cannot be found or read.
func readSentSide(c *client.Client, ref imapThreadRef) []imapMeta {
	name := findSentMailbox(c)
	if name == "" {
		return nil
	}
	status, err := c.Select(name, true)
	if err != nil {
		return nil
	}
	sent := imapMailboxRef{name: name, uidValidity: status.UidValidity, sent: true}
	uids, err := threadUIDs(c, ref, sent)
	if err != nil {
		return nil
	}
	metas, err := fetchIMAPMeta(c, newestUIDs(uids, imapMaxThreadMessages), sent, true)
	if err != nil {
		return nil
	}
	return metas
}

// withSession opens one authenticated IMAP session, runs fn, and closes it. Any
// failure is returned as a typed mailbox error: server text can carry the
// account name or a path, so it never leaves this package.
func (p *IMAPProvider) withSession(ctx context.Context, account Account, fn func(*client.Client) error) error {
	if p == nil || p.resolver == nil {
		return ErrDisconnected
	}
	creds, err := p.resolver.IMAPCredentials(ctx, account.ID)
	if err != nil {
		return err // already a typed mailbox error
	}
	if strings.TrimSpace(creds.Host) == "" || creds.Username == "" || creds.Password == "" {
		return ErrDisconnected
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, imapSessionTimeout)
		defer cancel()
	}

	c, err := p.dial(ctx, creds)
	if err != nil {
		return classifyIMAPError(ctx, err)
	}
	c.Timeout = imapCommandTimeout
	// The library logs protocol oddities to stderr by default; they can quote
	// server responses, so they are dropped rather than written to Ori's log.
	c.ErrorLog = log.New(io.Discard, "", 0)

	// A command blocked on a server that stopped answering must not outlive the
	// caller's deadline.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Terminate()
		case <-done:
		}
	}()
	defer func() { _ = c.Logout() }()

	if err := c.Login(creds.Username, creds.Password); err != nil {
		if ctx.Err() != nil || isIMAPConnectionError(err) {
			return classifyIMAPError(ctx, err)
		}
		// The server answered and said no: the saved password no longer works.
		return ErrExpired
	}
	return classifyIMAPError(ctx, fn(c))
}

// dial connects with TLS. Port 143 is the one clear-text start, and there the
// session must upgrade with STARTTLS before anything else: a password is never
// sent over an unencrypted connection.
func (p *IMAPProvider) dial(ctx context.Context, creds IMAPCredentials) (*client.Client, error) {
	host := strings.TrimSpace(creds.Host)
	port := creds.Port
	if port == 0 {
		port = imapImplicitTLSPort
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if port == imapStartTLSPort {
		return p.dialStartTLS(ctx, addr, host)
	}
	return p.dialImplicitTLS(ctx, addr, host)
}

func (p *IMAPProvider) tlsConfigFor(host string) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if p.tlsConfig != nil {
		cfg = p.tlsConfig.Clone()
	}
	if cfg.ServerName == "" {
		cfg.ServerName = host
	}
	return cfg
}

func (p *IMAPProvider) dialImplicitTLS(ctx context.Context, addr, host string) (*client.Client, error) {
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: imapDialTimeout}, Config: p.tlsConfigFor(host)}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return newIMAPClient(ctx, conn)
}

func (p *IMAPProvider) dialStartTLS(ctx context.Context, addr, host string) (*client.Client, error) {
	conn, err := (&net.Dialer{Timeout: imapDialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c, err := newIMAPClient(ctx, conn)
	if err != nil {
		return nil, err
	}
	if ok, err := c.SupportStartTLS(); err != nil || !ok {
		_ = c.Terminate()
		return nil, errIMAPNoEncryption
	}
	if err := c.StartTLS(p.tlsConfigFor(host)); err != nil {
		_ = c.Terminate()
		return nil, err
	}
	return c, nil
}

// errIMAPNoEncryption is internal: the server offered no way to encrypt the
// session, so no login was attempted.
var errIMAPNoEncryption = errors.New("mailbox: imap server offers no encryption")

// newIMAPClient reads the server greeting under the caller's deadline; the
// library has no timeout of its own for that first read.
func newIMAPClient(ctx context.Context, conn net.Conn) (*client.Client, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, err := client.New(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return c, nil
}

// threadUIDs finds one thread's messages in the selected mailbox.
func threadUIDs(c *client.Client, ref imapThreadRef, mailbox imapMailboxRef) ([]uint32, error) {
	if ref.rootID == "" {
		// A message with no Message-ID is its own thread, addressed by UID. A
		// changed UIDVALIDITY means the UID now names a different message.
		if mailbox.sent || mailbox.uidValidity != ref.uidValidity {
			return nil, nil
		}
		return []uint32{ref.uid}, nil
	}
	header := func(field string) *imap.SearchCriteria {
		criteria := imap.NewSearchCriteria()
		criteria.Header.Add(field, ref.rootID)
		return criteria
	}
	replies := imap.NewSearchCriteria()
	replies.Or = [][2]*imap.SearchCriteria{{header("References"), header("In-Reply-To")}}
	criteria := imap.NewSearchCriteria()
	criteria.Or = [][2]*imap.SearchCriteria{{header("Message-Id"), replies}}
	return c.UidSearch(criteria)
}

// fetchIMAPMeta reads the envelope, flags, and threading headers for uids in the
// selected mailbox, and with bodies set, each message's bounded text.
func fetchIMAPMeta(c *client.Client, uids []uint32, mailbox imapMailboxRef, bodies bool) ([]imapMeta, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	references := &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier, Fields: []string{"References"}},
		Peek:         true,
	}
	items := []imap.FetchItem{imap.FetchUid, imap.FetchFlags, imap.FetchInternalDate, imap.FetchEnvelope, references.FetchItem()}
	if bodies {
		items = append(items, imap.FetchBodyStructure)
	}

	var metas []imapMeta
	textParts := map[uint32]imapTextPart{}
	err := fetchIMAP(c, uids, items, func(msg *imap.Message) {
		meta := imapMetaFromMessage(msg, references, mailbox)
		metas = append(metas, meta)
		if bodies {
			if part, ok := pickIMAPTextPart(msg.BodyStructure); ok {
				textParts[msg.Uid] = part
			}
		}
	})
	if err != nil {
		return nil, err
	}
	if !bodies {
		return metas, nil
	}

	// Messages that keep their text in the same MIME part are fetched together,
	// so a thread costs a few round trips rather than one per message.
	byPath := map[string][]uint32{}
	for uid, part := range textParts {
		byPath[part.pathKey()] = append(byPath[part.pathKey()], uid)
	}
	text := map[uint32]string{}
	for _, group := range byPath {
		section := &imap.BodySectionName{
			BodyPartName: imap.BodyPartName{Path: textParts[group[0]].path},
			Peek:         true,
			Partial:      []int{0, imapMaxBodyBytes},
		}
		err := fetchIMAP(c, group, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, func(msg *imap.Message) {
			literal := msg.GetBody(section)
			if literal == nil {
				return
			}
			raw, _ := io.ReadAll(io.LimitReader(literal, imapMaxBodyBytes))
			text[msg.Uid] = decodeIMAPText(raw, textParts[msg.Uid])
		})
		if err != nil {
			return nil, err
		}
	}
	for i := range metas {
		metas[i].snippet = SanitizeText(StripQuotedHistory(text[metas[i].uid]), MaxSnippetLen)
	}
	return metas, nil
}

// fetchIMAP runs one UID FETCH and hands each message to visit. The channel is
// always drained, which the library requires before the next command.
func fetchIMAP(c *client.Client, uids []uint32, items []imap.FetchItem, visit func(*imap.Message)) error {
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	messages := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(set, items, messages) }()
	for msg := range messages {
		if msg != nil {
			visit(msg)
		}
	}
	return <-done
}

// findSentMailbox returns the folder holding the user's sent mail: the one the
// server marks \Sent, else a folder with a conventional name, else "".
func findSentMailbox(c *client.Client) string {
	mailboxes := make(chan *imap.MailboxInfo, 32)
	done := make(chan error, 1)
	go func() { done <- c.List("", "*", mailboxes) }()

	var marked, named string
	for info := range mailboxes {
		if info == nil || hasIMAPAttr(info.Attributes, imap.NoSelectAttr) {
			continue
		}
		switch {
		case hasIMAPAttr(info.Attributes, imap.SentAttr):
			if marked == "" {
				marked = info.Name
			}
		case named == "" && isConventionalSentName(info.Name, info.Delimiter):
			named = info.Name
		}
	}
	if err := <-done; err != nil {
		return ""
	}
	if marked != "" {
		return marked
	}
	return named
}

func hasIMAPAttr(attrs []string, want string) bool {
	for _, attr := range attrs {
		if strings.EqualFold(attr, want) {
			return true
		}
	}
	return false
}

// isConventionalSentName matches the last path segment of a folder name against
// the names mail servers use when they do not mark the folder.
func isConventionalSentName(name, delimiter string) bool {
	leaf := name
	if delimiter != "" {
		if i := strings.LastIndex(name, delimiter); i >= 0 {
			leaf = name[i+len(delimiter):]
		}
	}
	switch strings.ToLower(strings.TrimSpace(leaf)) {
	case "sent", "sent mail", "sent messages", "sent items":
		return true
	}
	return false
}

// newestUIDs returns at most limit of the highest UIDs, ascending. UIDs only
// grow within a mailbox, so the highest are the most recently arrived.
func newestUIDs(uids []uint32, limit int) []uint32 {
	out := append([]uint32(nil), uids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// classifyIMAPError maps a session failure to a typed mailbox error. The raw
// error is dropped: IMAP status text is written by the server and may name the
// account, a folder, or an internal host.
func classifyIMAPError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	for _, typed := range []error{ErrDisconnected, ErrExpired, ErrPermissionDenied, ErrRateLimited, ErrTimeout, ErrNotFound, ErrProvider} {
		if errors.Is(err, typed) {
			return err
		}
	}
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrTimeout
	}
	return ErrProvider
}

// isIMAPConnectionError reports whether a failed command never got an answer,
// as opposed to the server answering NO. It keeps a dropped connection during
// login from being reported as a wrong password.
func isIMAPConnectionError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return strings.HasPrefix(err.Error(), "imap: connection closed")
}
