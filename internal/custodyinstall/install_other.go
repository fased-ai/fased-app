//go:build !linux

package custodyinstall

import (
	"context"
	"errors"
	"io"
)

type System struct {
	In  io.Reader
	Out io.Writer
}

var unsupported = errors.New("installer-managed custody identities are supported only by the Ubuntu fixture; wallets remain disabled")

func Install(context.Context, Options, *System) (Manifest, error)                { return Manifest{}, unsupported }
func Load(int) (Manifest, error)                                                 { return Manifest{}, unsupported }
func Verify(Manifest) error                                                      { return unsupported }
func Runtime(Manifest) error                                                     { return unsupported }
func Remove(context.Context, Manifest, *System) error                            { return unsupported }
func Control(context.Context, Manifest, string, string, *System) error           { return unsupported }
func (*System) AppHelper(context.Context, Manifest, bool, bool, io.Writer) error { return unsupported }

func OwnerLock() (io.Closer, error) { return nil, unsupported }

func (*System) AccountsHelper(context.Context, Manifest, bool, bool, bool, io.Writer) error {
	return unsupported
}
