package mailbox

import (
	"context"
	"errors"
	"testing"
)

type fixedTransports map[string]Transport

func (f fixedTransports) Transport(_ context.Context, accountID string) (Transport, error) {
	if t, ok := f[accountID]; ok {
		return t, nil
	}
	return "", ErrDisconnected
}

// namedReader answers every read with its own name as the thread subject.
type namedReader string

func (n namedReader) SearchThreads(context.Context, Account, Query) (ThreadPage, error) {
	return ThreadPage{Threads: []Thread{{Subject: string(n)}}}, nil
}

func (n namedReader) GetThread(context.Context, Account, string) (Thread, error) {
	return Thread{Subject: string(n)}, nil
}

func TestRoutingProviderSendsEachAccountToItsTransport(t *testing.T) {
	router := NewRoutingProvider(fixedTransports{"google": TransportGmailAPI, "password": TransportIMAP}, namedReader("gmail"), namedReader("imap"))
	for account, want := range map[string]string{"google": "gmail", "password": "imap"} {
		page, err := router.SearchThreads(context.Background(), Account{ID: account}, Query{})
		if err != nil || page.Threads[0].Subject != want {
			t.Fatalf("SearchThreads(%s) = %+v, %v; want the %s reader", account, page, err, want)
		}
		thread, err := router.GetThread(context.Background(), Account{ID: account}, "t")
		if err != nil || thread.Subject != want {
			t.Fatalf("GetThread(%s) = %+v, %v; want the %s reader", account, thread, err, want)
		}
	}
}

func TestRoutingProviderNeverFallsBackToTheOtherReader(t *testing.T) {
	router := NewRoutingProvider(fixedTransports{"password": TransportIMAP, "odd": Transport("pop3")}, namedReader("gmail"), nil)
	for _, account := range []string{"password", "odd", "missing"} {
		if _, err := router.SearchThreads(context.Background(), Account{ID: account}, Query{}); !errors.Is(err, ErrDisconnected) {
			t.Fatalf("SearchThreads(%s) err = %v, want ErrDisconnected", account, err)
		}
	}
	if _, err := (*RoutingProvider)(nil).GetThread(context.Background(), Account{ID: "x"}, "t"); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil router err = %v", err)
	}
}
