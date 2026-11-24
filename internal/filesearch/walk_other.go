//go:build !darwin

package filesearch

import (
	"context"
	"time"
)

func rootExistedAt(string, string, time.Time) bool { return false }

func dispatch(context.Context, Source, entry, bool, func(context.Context, string, string) error) error {
	return ErrUnavailable
}

func rootWitness(string) (string, error)                      { return "", ErrUnavailable }
func walk(context.Context, Source, func([]entry) error) error { return ErrUnavailable }
