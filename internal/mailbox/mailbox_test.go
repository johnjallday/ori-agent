package mailbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeProvider is the contract fixture used to exercise the provider-neutral
// surface without a real Gmail backend (task 3.1). It records the last query it
// received (after the caller's normalization) and returns canned results/errors.
type fakeProvider struct {
	page    ThreadPage
	err     error
	lastQ   Query
	lastAcc Account
}

func (f *fakeProvider) SearchThreads(ctx context.Context, account Account, q Query) (ThreadPage, error) {
	f.lastAcc = account
	f.lastQ = q
	if f.err != nil {
		return ThreadPage{}, f.err
	}
	return f.page, nil
}

func (f *fakeProvider) GetThread(ctx context.Context, account Account, threadID string) (Thread, error) {
	if f.err != nil {
		return Thread{}, f.err
	}
	for _, th := range f.page.Threads {
		if th.ID == threadID {
			return th, nil
		}
	}
	return Thread{}, ErrNotFound
}

// assert fakeProvider satisfies the interface.
var _ MailboxProvider = (*fakeProvider)(nil)

func TestQueryNormalizedClampsBounds(t *testing.T) {
	got := Query{MaxResults: 1000, LookbackDays: 999}.Normalized()
	if got.MaxResults != MaxThreadsPerQuery {
		t.Fatalf("MaxResults not clamped: %d", got.MaxResults)
	}
	if got.LookbackDays != MaxLookbackDays {
		t.Fatalf("LookbackDays not clamped: %d", got.LookbackDays)
	}

	def := Query{}.Normalized()
	if def.MaxResults != DefaultMaxResults || def.LookbackDays != DefaultLookbackDays {
		t.Fatalf("defaults not applied: %+v", def)
	}
}

func TestEmptyPageIsHealthyNotError(t *testing.T) {
	f := &fakeProvider{page: ThreadPage{Threads: nil}}
	page, err := f.SearchThreads(context.Background(), Account{ID: "a1"}, Query{}.Normalized())
	if err != nil {
		t.Fatalf("empty result must not be an error: %v", err)
	}
	if len(page.Threads) != 0 || page.NextPageToken != "" {
		t.Fatalf("expected a healthy empty page, got %+v", page)
	}
}

func TestTypedErrorsPropagateAndClassify(t *testing.T) {
	f := &fakeProvider{err: ErrExpired}
	_, err := f.SearchThreads(context.Background(), Account{}, Query{})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
	if HealthForError(err) != HealthExpired {
		t.Fatalf("expired error should map to HealthExpired, got %v", HealthForError(err))
	}
	if HealthForError(nil) != HealthHealthy {
		t.Fatal("nil error should map to HealthHealthy")
	}
	// A rate limit / timeout does not change connection health.
	if HealthForError(ErrRateLimited) != HealthHealthy || HealthForError(ErrTimeout) != HealthHealthy {
		t.Fatal("transient errors should not change health")
	}
}

func TestRateLimitErrorCarriesRetryAfterAndUnwraps(t *testing.T) {
	var err error = &RateLimitError{RetryAfter: 30 * time.Second}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatal("RateLimitError must unwrap to ErrRateLimited")
	}
	if !IsTransient(err) {
		t.Fatal("rate limit must be transient")
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 30*time.Second {
		t.Fatalf("retry-after not preserved: %+v", rl)
	}
	if IsTransient(ErrDisconnected) {
		t.Fatal("disconnected is terminal, not transient")
	}
}

func TestGetThreadNotFound(t *testing.T) {
	f := &fakeProvider{page: ThreadPage{Threads: []Thread{{ID: "t1"}}}}
	if _, err := f.GetThread(context.Background(), Account{}, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	th, err := f.GetThread(context.Background(), Account{}, "t1")
	if err != nil || th.ID != "t1" {
		t.Fatalf("expected thread t1, got %+v err=%v", th, err)
	}
}
