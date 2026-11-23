package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"golang.org/x/term"
)

func aiConnectionCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("ai-connection", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private instance directory")
	stdin := f.Bool("request-stdin", false, "one strict credential command from a private pipe; never arguments")
	remote := f.Bool("remote", false, "connect to the active remote instance over private HTTPS")
	urlFlag := f.String("url", "", "explicit remote instance URL (requires --remote and --instance-id)")
	instanceID := f.String("instance-id", "", "explicit remote instance identity (requires --remote and --url)")
	signIn := f.String("sign-in", "", "sign in remotely using local browser callback bridge (openai|google|openrouter)")
	name := f.String("name", "", "optional label for the new remote sign-in connection")
	googleProject := f.String("google-project", "", "Google Cloud project ID when using --sign-in google")
	noBrowser := f.Bool("no-browser", false, "print the remote sign-in URL without auto-launching a local browser")
	passphraseStdin := f.Bool("passphrase-stdin", false, "read remote vault passphrase from private stdin")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("use read-only status, --request-stdin, or --remote --sign-in; never pass secrets as arguments")
	}
	if (*urlFlag != "") != (*instanceID != "") {
		return errors.New("pass both --url and --instance-id together, or omit both to use the connected remote profile")
	}
	if *urlFlag != "" && !*remote {
		return errors.New("--url and --instance-id require --remote")
	}
	if *signIn != "" && !*remote {
		return errors.New("--sign-in requires --remote")
	}
	if *signIn != "" && *stdin {
		return errors.New("choose either --sign-in or --request-stdin")
	}
	if (*name != "" || *googleProject != "" || *noBrowser || *passphraseStdin) && *signIn == "" {
		return errors.New("sign-in options require --sign-in")
	}

	timeout := 10 * time.Second
	if *signIn != "" {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var (
		c *core.Client
	)
	if *remote {
		if *urlFlag != "" {
			c, e = aiConnectionDirectRemoteClient(ctx, *urlFlag, *instanceID)
		} else {
			c, e = aiConnectionRemoteClient(ctx, *dir)
		}
		if e != nil {
			return e
		}
		defer c.Close()
		if *signIn != "" {
			return runRemoteSignInBridge(ctx, c, remoteSignInBridgeOpts{
				provider:        *signIn,
				name:            *name,
				googleProject:   *googleProject,
				noBrowser:       *noBrowser,
				passphraseStdin: *passphraseStdin,
			}, in, out, errOut)
		}
	} else {
		c, e = core.NewClient(ctx, *dir)
		if e != nil {
			return e
		}
		defer c.Close()
		if e = c.LoadSession(ctx, *dir); e != nil {
			return e
		}
	}
	var v core.AIConnectionView
	if *stdin {
		if term.IsTerminal(int(in.Fd())) {
			return errors.New("credential JSON requires a private pipe, not an echoing terminal")
		}
		b, readErr := io.ReadAll(io.LimitReader(in, 4097))
		if readErr != nil {
			return readErr
		}
		defer clear(b)
		if len(b) > 4096 {
			return errors.New("oversized credential command")
		}
		var command core.AIConnectionCommand
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&command) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("one strict credential command required")
		}
		e = c.Do(ctx, http.MethodPost, "/api/v1/ai/connection", command, &v)
	} else {
		e = c.Do(ctx, http.MethodGet, "/api/v1/ai/connection", nil, &v)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(v)
}
