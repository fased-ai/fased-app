//go:build !linux && !darwin && !windows

package localinstall

import (
	"context"
	"errors"
	"path/filepath"
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

const engineName = "fased-app"
const desktopName = "fased-desktop"
const definitionName = "unsupported.service"

func defaultRoot(home string) string { return filepath.Join(home, ".local", "share", "fased-app") }
