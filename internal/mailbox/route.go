package mailbox

import "context"

// Transport is how an account's mail is read. It follows from how the account
// was connected, not from who hosts it: a Gmail address connected with Ori's
// Google sign-in is read through the Gmail API, and the same address connected
// with an app password is read over IMAP.
type Transport string

const (
	TransportGmailAPI Transport = "gmail_api"
	TransportIMAP     Transport = "imap"
)

// TransportResolver reports an account's transport. It answers ErrDisconnected
// for an account that is missing or was connected in a way no reader supports.
type TransportResolver interface {
	Transport(ctx context.Context, accountID string) (Transport, error)
}

// RoutingProvider is the one MailboxProvider the runtime holds. It sends each
// read to the reader for that account's transport, so the brief, the agent
// tools, and reply composing never need to know how an account was connected.
type RoutingProvider struct {
	transports TransportResolver
	gmail      MailboxProvider
	imap       MailboxProvider
}

// NewRoutingProvider builds the router. A nil reader leaves its transport
// unavailable rather than falling back to the other one.
func NewRoutingProvider(transports TransportResolver, gmail, imap MailboxProvider) *RoutingProvider {
	return &RoutingProvider{transports: transports, gmail: gmail, imap: imap}
}

var _ MailboxProvider = (*RoutingProvider)(nil)

func (r *RoutingProvider) SearchThreads(ctx context.Context, account Account, q Query) (ThreadPage, error) {
	reader, err := r.route(ctx, account)
	if err != nil {
		return ThreadPage{}, err
	}
	return reader.SearchThreads(ctx, account, q)
}

func (r *RoutingProvider) GetThread(ctx context.Context, account Account, threadID string) (Thread, error) {
	reader, err := r.route(ctx, account)
	if err != nil {
		return Thread{}, err
	}
	return reader.GetThread(ctx, account, threadID)
}

func (r *RoutingProvider) route(ctx context.Context, account Account) (MailboxProvider, error) {
	if r == nil || r.transports == nil {
		return nil, ErrDisconnected
	}
	transport, err := r.transports.Transport(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	var reader MailboxProvider
	switch transport {
	case TransportGmailAPI:
		reader = r.gmail
	case TransportIMAP:
		reader = r.imap
	}
	if reader == nil {
		return nil, ErrDisconnected
	}
	return reader, nil
}
