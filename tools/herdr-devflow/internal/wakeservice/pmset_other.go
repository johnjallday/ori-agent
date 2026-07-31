//go:build !darwin

package wakeservice

import (
	"context"
	"time"
)

func defaultPMSetRunner(context.Context, []string) ([]byte, error) {
	return nil, ErrUnsupported
}

type unsupportedPowerScheduler struct{}

func (unsupportedPowerScheduler) Events(context.Context) ([]PowerEvent, error) {
	return nil, ErrUnsupported
}

func (unsupportedPowerScheduler) Schedule(context.Context, time.Time) error {
	return ErrUnsupported
}

func (unsupportedPowerScheduler) Cancel(context.Context, time.Time) error {
	return ErrUnsupported
}
