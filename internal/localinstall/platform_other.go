//go:build !linux

package localinstall

import (
	"context"
	"errors"
)

func supported() error {
	return errors.New("native service installation is currently available on Linux only")
}
func definition(Paths, Options) ([]byte, error)       { return nil, supported() }
func register(context.Context, Manifest) error        { return supported() }
func enable(context.Context, Manifest) error          { return supported() }
func start(context.Context, Manifest) error           { return supported() }
func stop(context.Context, Manifest) error            { return supported() }
func status(context.Context, Manifest) (State, error) { return State{}, supported() }
func unregister(context.Context, Manifest) error      { return supported() }
