//go:build !darwin

package filesearch

import "context"

func rootWitness(string) (string, error)                      { return "", ErrUnavailable }
func walk(context.Context, Source, func([]entry) error) error { return ErrUnavailable }
