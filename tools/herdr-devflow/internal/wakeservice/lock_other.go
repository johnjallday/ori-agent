//go:build !darwin

package wakeservice

import (
	"context"
	"sync"
)

// nonDarwinTestLock deliberately exists only for injected, non-root services
// used by portable protocol tests. The production daemon enters through
// ServeDefault, which refuses unsupported platforms before any filesystem or
// power action. A Config with RequireRoot=true stays unsupported here.
var nonDarwinTestLock = make(chan struct{}, 1)

var initializeNonDarwinTestLock sync.Once

func acquireRootLock(ctx context.Context, _ string, requireRoot bool) (func(), error) {
	if requireRoot {
		return nil, ErrUnsupported
	}
	initializeNonDarwinTestLock.Do(func() { nonDarwinTestLock <- struct{}{} })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-nonDarwinTestLock:
		return func() { nonDarwinTestLock <- struct{}{} }, nil
	}
}
