//go:build !linux

package main

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
)

func prepareRemoteUpdateAuthorityPlatform(context.Context, vpsinstall.Manifest, *custodyinstall.System, io.Writer) error {
	return errors.New("Remote maintenance preparation requires Ubuntu")
}
