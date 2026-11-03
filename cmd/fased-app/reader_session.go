package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"io"
	"os"
	"time"
)

func readerSessionClient(args []string, out io.Writer) error {
	if len(args) != 4 || args[0] != "--config" || args[2] != "--binding" {
		return errors.New("fixed Remote source session helper required")
	}
	image, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := vpsinstall.ValidateSourceSessionHelper(args[1], image, args[3], os.Getuid())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := core.NewClient(ctx, m.Data)
	if err != nil {
		return err
	}
	defer client.Close()
	if err = loadRemoteHelperSession(ctx, client, m); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Ready")
	return err
}
func prepareRemoteSourceSession(ctx context.Context, m vpsinstall.Manifest, system *custodyinstall.System) error {
	image, err := os.Executable()
	if err != nil {
		return err
	}
	return vpsinstall.PrepareSourceSession(ctx, m, image, system)
}
