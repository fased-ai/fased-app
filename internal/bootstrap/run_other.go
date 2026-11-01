//go:build !linux

package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
)

func owned(os.FileInfo) bool { return false }

type InstallOptions struct {
	Prefix, DataDirectory string
	Start                 bool
	Profile, Listen       string
	upgrade               *UpgradeOptions
}

func (*Prepared) Run(context.Context, InstallOptions, io.Reader, io.Writer, io.Writer) error {
	return errors.New("initial bootstrap execution supports Linux amd64 only")
}

func (*Prepared) RunUpgrade(context.Context, UpgradeOptions, io.Reader, io.Writer, io.Writer) error {
	return errors.New("installed upgrade execution supports Linux amd64 only")
}
