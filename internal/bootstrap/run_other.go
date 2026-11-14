//go:build !linux

package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/localinstall"
)

func owned(os.FileInfo) bool { return false }

type InstallOptions struct {
	WalletSigner                  *WalletPayload
	WalletInstance, WalletNetwork string
	Prefix, DataDirectory         string
	Start                         bool
	Profile, Listen               string
	upgrade                       *UpgradeOptions
}

func FreshCandidatePaths(InstallOptions) (localinstall.Paths, error) {
	return localinstall.Paths{}, errors.New("candidate execution supports Linux amd64 only")
}

func (*Prepared) Run(context.Context, InstallOptions, io.Reader, io.Writer, io.Writer) error {
	return errors.New("initial bootstrap execution supports Linux amd64 only")
}

func (*Prepared) VerifyFreshCandidateInstaller(InstallOptions) error {
	return errors.New("candidate execution supports Linux amd64 only")
}

func (*Prepared) RunUpgrade(context.Context, UpgradeOptions, io.Reader, io.Writer, io.Writer) error {
	return errors.New("installed upgrade execution supports Linux amd64 only")
}
