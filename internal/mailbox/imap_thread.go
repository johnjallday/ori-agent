package mailbox

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime/quotedprintable"
	"net/textproto"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-message/charset"
)

// Thread reconstruction for IMAP. The protocol has messages, not threads, so a
// conversation is whatever the reply headers say it is: every message is filed
// under the first Message-ID in its References header (the message that started
// the exchange), or under its own Message-ID when it replies to nothing.
//
// Subjects are deliberately not used. Two unrelated "Invoice" emails share a
// subject and nothing else, and merging them would put one sender's message in
// another sender's thread.

const (
	// A thread ID names its conversation by the root Message-ID, so GetThread
	// can find the thread again without a table that a restart would lose.
	imapThreadByMessageID = "m."
	// A message with no Message-ID is its own thread, named by UID.
	imapThreadByUID = "u."
	// imapMaxMessageIDLen bounds a Message-ID Ori will carry in a thread ID.
	imapMaxMessageIDLen = 512
)

var errIMAPBadThreadID = errors.New("mailbox: malformed imap thread id")

// messageIDPattern matches one <id> in a Message-ID, In-Reply-To, or References
// header value.
var messageIDPattern = regexp.MustCompile(`<([^<>\s]+)>`)

// imapMailboxRef is the folder a batch of messages was read from.
type imapMailboxRef struct {
	name        string
	uidValidity uint32
	// sent marks the user's Sent folder: everything in it is from the user.
	sent bool
}

// imapThreadRef is a parsed thread ID: a root Message-ID, or a single UID.
type imapThreadRef struct {
	rootID      string
	uidValidity uint32
	uid         uint32
}

// imapMeta is one message as read from the server, before it is projected into
// the neutral Message type.
type imapMeta struct {
	uid         uint32
	uidValidity uint32
	sent        bool
	messageID   string
	// parents are the Message-IDs this message replies to, oldest first: the
	// References header, then In-Reply-To.
	parents       []string
	subject       string
	from          Participant
	to            []Participant
	cc            []Participant
	date          time.Time
	unread        bool
	answered      bool
	bulk          bool
	autoSubmitted bool
	snippet       string
}

// rootID is the Message-ID of the message that started this conversation, or ""
// when the message carries no usable ID at all.
func (m imapMeta) rootID() string {
	if len(m.parents) > 0 {
		return m.parents[0]
	}
	return m.messageID
}

func (m imapMeta) threadID() string {
	if root := m.rootID(); root != "" {
		return imapThreadByMessageID + base64.RawURLEncoding.EncodeToString([]byte(root))
	}
	return m.uidID()
}

func (m imapMeta) uidID() string {
	return imapThreadByUID + strconv.FormatUint(uint64(m.uidValidity), 10) + "." + strconv.FormatUint(uint64(m.uid), 10)
}

// providerID is the stable message identity: its Message-ID when it has one.
func (m imapMeta) providerID() string {
	if m.messageID != "" {
		return m.messageID
	}
	return m.uidID()
}

// parseIMAPThreadID reverses threadID. Thread IDs come back from agents and
// browsers, so anything that does not parse exactly is rejected.
func parseIMAPThreadID(id string) (imapThreadRef, error) {
	id = strings.TrimSpace(id)
	switch {
	case strings.HasPrefix(id, imapThreadByMessageID):
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, imapThreadByMessageID))
		if err != nil {
			return imapThreadRef{}, errIMAPBadThreadID
		}
		root := string(raw)
		if !usableMessageID(root) {
			return imapThreadRef{}, errIMAPBadThreadID
		}
		return imapThreadRef{rootID: root}, nil
	case strings.HasPrefix(id, imapThreadByUID):
		validity, uid, ok := strings.Cut(strings.TrimPrefix(id, imapThreadByUID), ".")
		if !ok {
			return imapThreadRef{}, errIMAPBadThreadID
		}
		v, errV := strconv.ParseUint(validity, 10, 32)
		u, errU := strconv.ParseUint(uid, 10, 32)
		if errV != nil || errU != nil || u == 0 {
			return imapThreadRef{}, errIMAPBadThreadID
		}
		return imapThreadRef{uidValidity: uint32(v), uid: uint32(u)}, nil
	}
	return imapThreadRef{}, errIMAPBadThreadID
}

// usableMessageID reports whether id can be carried in a thread ID and sent back
// to the server as a search string.
func usableMessageID(id string) bool {
	if id == "" || len(id) > imapMaxMessageIDLen {
		return false
	}
	return !strings.ContainsAny(id, "<> \t\r\n\x00")
}

// messageIDs extracts the Message-IDs from a header value, in order, without
// their angle brackets. Unusable IDs are dropped.
func messageIDs(value string) []string {
	var out []string
	for _, match := range messageIDPattern.FindAllStringSubmatch(value, -1) {
		if usableMessageID(match[1]) {
			out = append(out, match[1])
		}
	}
	return out
}

// firstMessageID reads a message's own Message-ID. Some mailers write it without
// the angle brackets; replies still quote it bracketed, so the bare form is
// accepted here or the conversation would split in two.
func firstMessageID(value string) string {
	if ids := messageIDs(value); len(ids) > 0 {
		return ids[0]
	}
	if bare := strings.TrimSpace(value); strings.Contains(bare, "@") && usableMessageID(bare) {
		return bare
	}
	return ""
}

// imapHeaderSection fetches the headers the envelope does not carry: the
// threading header and the list and automation signals. PEEK keeps the
// message unread.
func imapHeaderSection() *imap.BodySectionName {
	fields := append([]string{"References"}, signalHeaders...)
	return &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier, Fields: fields},
		Peek:         true,
	}
}

// parseIMAPHeaders reads the fetched header block. A block cut at the size
// limit still yields every header before the cut.
func parseIMAPHeaders(raw []byte) textproto.MIMEHeader {
	reader := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(raw, '\r', '\n', '\r', '\n'))))
	header, _ := reader.ReadMIMEHeader()
	if header == nil {
		return textproto.MIMEHeader{}
	}
	return header
}

// imapMetaFromMessage projects one fetched message. Every text field is
// untrusted and is sanitized here, before anything above this package sees it.
func imapMetaFromMessage(msg *imap.Message, headers *imap.BodySectionName, mailbox imapMailboxRef) imapMeta {
	meta := imapMeta{
		uid:         msg.Uid,
		uidValidity: mailbox.uidValidity,
		sent:        mailbox.sent,
		date:        msg.InternalDate.UTC(),
		unread:      true,
	}
	for _, flag := range msg.Flags {
		switch imap.CanonicalFlag(flag) {
		case imap.SeenFlag:
			meta.unread = false
		case imap.AnsweredFlag:
			meta.answered = true
		}
	}

	var inReplyTo []string
	if env := msg.Envelope; env != nil {
		meta.subject = SanitizeText(env.Subject, 500)
		meta.messageID = firstMessageID(env.MessageId)
		inReplyTo = messageIDs(env.InReplyTo)
		if len(env.From) > 0 {
			meta.from = imapParticipant(env.From[0])
		}
		meta.to = imapParticipants(env.To)
		meta.cc = imapParticipants(env.Cc)
		if msg.InternalDate.IsZero() {
			meta.date = env.Date.UTC()
		}
	}
	if literal := msg.GetBody(headers); literal != nil {
		raw, _ := io.ReadAll(io.LimitReader(literal, imapMaxHeaderBytes))
		header := parseIMAPHeaders(raw)
		meta.parents = messageIDs(header.Get("References"))
		meta.bulk, meta.autoSubmitted = senderSignals(header.Get)
	}
	meta.parents = append(meta.parents, inReplyTo...)
	return meta
}

func imapParticipants(addrs []*imap.Address) []Participant {
	var out []Participant
	for _, addr := range addrs {
		if p := imapParticipant(addr); p.Address != "" {
			out = append(out, p)
		}
	}
	return out
}

func imapParticipant(addr *imap.Address) Participant {
	if addr == nil || addr.MailboxName == "" || addr.HostName == "" {
		return Participant{}
	}
	return Participant{Name: SanitizeText(addr.PersonalName, 200), Address: addr.Address()}
}

// imapThreadGroup is one reconstructed conversation.
type imapThreadGroup struct {
	id       string
	messages []imapMeta
	// lastUID is the highest inbox UID in the thread: its position in arrival
	// order, and the list's pagination cursor.
	lastUID uint32
}

// groupIMAPThreads files messages into conversations, newest conversation
// first. A message present in both the inbox and the Sent folder appears once.
func groupIMAPThreads(metas []imapMeta) []imapThreadGroup {
	byID := map[string]*imapThreadGroup{}
	var order []string
	for _, meta := range metas {
		id := meta.threadID()
		group, ok := byID[id]
		if !ok {
			group = &imapThreadGroup{id: id}
			byID[id] = group
			order = append(order, id)
		}
		if meta.messageID != "" && group.has(meta.messageID) {
			continue
		}
		group.messages = append(group.messages, meta)
		if !meta.sent && meta.uid > group.lastUID {
			group.lastUID = meta.uid
		}
	}

	groups := make([]imapThreadGroup, 0, len(order))
	for _, id := range order {
		group := byID[id]
		sort.SliceStable(group.messages, func(i, j int) bool {
			return group.messages[i].date.Before(group.messages[j].date)
		})
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].lastUID > groups[j].lastUID })
	return groups
}

func (g *imapThreadGroup) has(messageID string) bool {
	for _, m := range g.messages {
		if m.messageID == messageID {
			return true
		}
	}
	return false
}

// thread projects the group into the neutral Thread.
func (g imapThreadGroup) thread(account Account) Thread {
	out := Thread{ID: g.id, AccountID: account.ID}
	seen := map[string]struct{}{}
	for _, m := range g.messages {
		msg := Message{
			ID:       m.providerID(),
			ThreadID: g.id,
			From:     m.from,
			To:       m.to,
			Cc:       m.cc,
			Subject:  m.subject,
			Snippet:  m.snippet,
			SentAt:   m.date,
			FromUser: m.sent || (account.EmailAddress != "" && strings.EqualFold(m.from.Address, account.EmailAddress)),
			// A copy in the Sent folder is the user's own message; its flags say
			// nothing about what the user still has to read.
			Unread:        m.unread && !m.sent,
			Answered:      m.answered,
			Bulk:          m.bulk,
			AutoSubmitted: m.autoSubmitted,
		}
		out.Messages = append(out.Messages, msg)
		if out.Subject == "" && msg.Subject != "" {
			out.Subject = msg.Subject
		}
		if address := strings.ToLower(msg.From.Address); address != "" {
			if _, dup := seen[address]; !dup {
				seen[address] = struct{}{}
				out.Participants = append(out.Participants, msg.From)
			}
		}
		if msg.SentAt.After(out.LastMessageAt) {
			out.LastMessageAt = msg.SentAt
		}
		if msg.Unread {
			out.Unread = true
		}
	}
	if n := len(g.messages); n > 0 {
		last := out.Messages[n-1]
		// The inbox listing does not read the Sent folder, so the user's reply
		// is invisible there. The server's \Answered flag is the same fact.
		out.WaitingOnUser = !last.FromUser && out.Unread && !g.messages[n-1].answered
	}
	return out
}

// imapTextPart locates a message's readable text inside its MIME structure.
type imapTextPart struct {
	path     []int
	encoding string
	charset  string
}

func (p imapTextPart) pathKey() string {
	parts := make([]string, len(p.path))
	for i, n := range p.path {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ".")
}

// pickIMAPTextPart chooses the part to read: the first text/plain, else the
// first text/html (which the sanitizer reduces to text). Attachments and
// attached emails are never chosen, so they are never downloaded.
func pickIMAPTextPart(bs *imap.BodyStructure) (imapTextPart, bool) {
	if bs == nil {
		return imapTextPart{}, false
	}
	var plain, html *imapTextPart
	bs.Walk(func(path []int, part *imap.BodyStructure) bool {
		if strings.EqualFold(part.MIMEType, "multipart") {
			return true
		}
		if !strings.EqualFold(part.MIMEType, "text") || strings.EqualFold(part.Disposition, "attachment") {
			return false
		}
		candidate := &imapTextPart{
			path:     append([]int(nil), path...),
			encoding: part.Encoding,
			charset:  mimeParam(part.Params, "charset"),
		}
		switch strings.ToLower(part.MIMESubType) {
		case "plain":
			if plain == nil {
				plain = candidate
			}
		case "html":
			if html == nil {
				html = candidate
			}
		}
		return false
	})
	switch {
	case plain != nil:
		return *plain, true
	case html != nil:
		return *html, true
	}
	return imapTextPart{}, false
}

func mimeParam(params map[string]string, name string) string {
	for key, value := range params {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// decodeIMAPText undoes a part's transfer encoding and charset. The body may
// have been cut at the fetch limit, mid-line or mid-sequence; whatever decoded
// before the cut is still the start of the message, so a decode error at the
// end is not a failure.
func decodeIMAPText(raw []byte, part imapTextPart) string {
	var r io.Reader = bytes.NewReader(raw)
	switch strings.ToLower(strings.TrimSpace(part.encoding)) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	if name := strings.TrimSpace(part.charset); name != "" {
		if decoded, err := charset.Reader(name, r); err == nil {
			r = decoded
		}
	}
	text, _ := io.ReadAll(io.LimitReader(r, imapMaxBodyBytes))
	return strings.ToValidUTF8(string(text), "")
}

// The list cursor is the lowest thread position already returned.
func encodeIMAPPageToken(lastUID uint32) string {
	return strconv.FormatUint(uint64(lastUID), 10)
}

func decodeIMAPPageToken(token string) (uint32, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, nil
	}
	uid, err := strconv.ParseUint(token, 10, 32)
	if err != nil || uid == 0 {
		return 0, errors.New("mailbox: malformed imap page token")
	}
	return uint32(uid), nil
}
