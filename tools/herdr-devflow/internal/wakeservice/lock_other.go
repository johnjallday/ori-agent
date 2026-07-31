//go:build !darwin

package wakeservice

import (
	"context"
)

func acquireRootLock(context.Context, string, bool) (func(), error) {
	return nil, ErrUnsupported
}
