package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fased-ai/fased-app/desktop/internal/window"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/wailsapp/wails/v2"
)

func main() {
	dir, err := core.DefaultDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot locate private core state")
		os.Exit(1)
	}
	state := flag.String("data-dir", dir, "private instance state directory")
	version := flag.Bool("version", false, "show version")
	flag.Parse()
	if *version {
		fmt.Println(core.Version)
		return
	}
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(1)
	}
	if paths, err := localinstall.DefaultPaths(); err == nil && paths.Data == *state {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = localinstall.Start(ctx, paths)
		cancel()
	}
	// This window owns no engine process. Closing it never stops the OS service.
	err = wails.Run(window.Options(*state))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Desktop window unavailable:", err)
		os.Exit(1)
	}
}
