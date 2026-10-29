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

	"github.com/fased-ai/fased-app/internal/core"
)

func accountsCommand(args []string, in *os.File, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("accounts", flag.ContinueOnError)
	flags.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := flags.String("data-dir", base, "private instance directory")
	backupOut := flags.String("rpc-backup-out", "", "new private encrypted RPC backup file")
	rpcSecret := flags.Bool("rpc-credential-stdin", false, "manage disposable RPC credentials from private JSON input")
	rpcConfig := flags.Bool("rpc-stdin", false, "save/remove disposable wallet RPC settings from JSON")
	rpcCheck := flags.Bool("check-rpc-stdin", false, "check disposable RPC endpoint/network JSON; no saving or live calls")
	update := flags.Bool("request-stdin", false, "revision-checked public account record JSON from stdin; no secrets")
	setup := flags.Bool("setup-stdin", false, "add a network public address or verified signer reference from JSON; no secrets")
	if e = flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected account arguments")
	}
	if *backupOut != "" && !*rpcSecret {
		return errors.New("backup output requires RPC credential input")
	}
	ctx := context.Background()
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = c.LoadSession(ctx, *dir); e != nil {
		return e
	}
	if *setup {
		if *rpcSecret || *rpcConfig || *rpcCheck || *update || *backupOut != "" {
			return errors.New("choose one account operation")
		}
		b, err := io.ReadAll(io.LimitReader(in, 2049))
		if err != nil || len(b) > 2048 {
			return errors.New("invalid public account setup")
		}
		var input core.AccountSetupCommand
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid public account setup; no key or password fields accepted")
		}
		var result core.AccountsView
		if err = c.Do(ctx, http.MethodPut, "/api/v1/accounts/setup", input, &result); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *rpcSecret {
		if *rpcConfig || *rpcCheck || *update {
			return errors.New("choose one account operation")
		}
		b, err := io.ReadAll(io.LimitReader(in, 8193))
		defer clear(b)
		if err != nil || len(b) > 8192 {
			return errors.New("invalid credential input")
		}
		var input core.RPCCredentialCommand
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid credential input")
		}
		if (input.Action == "backup") != (*backupOut != "") {
			return errors.New("backup requires a new private output file")
		}
		var result core.RPCCredentialView
		if err = c.Do(ctx, http.MethodPut, "/api/v1/accounts/rpc-credential", input, &result); err != nil {
			return err
		}
		if input.Action == "backup" {
			if err = core.WriteRPCBackup(*backupOut, result.Backup); err != nil {
				return err
			}
			result.Backup = nil
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *rpcConfig {
		if *rpcCheck || *update {
			return errors.New("choose one account operation")
		}
		b, err := io.ReadAll(io.LimitReader(in, 2049))
		if err != nil || len(b) > 2048 {
			return errors.New("invalid RPC settings")
		}
		var input core.RPCConfigInput
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid RPC settings; no credentials accepted")
		}
		var result core.AccountsView
		if err = c.Do(ctx, http.MethodPut, "/api/v1/accounts/rpc", input, &result); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *rpcCheck {
		if *update {
			return errors.New("choose account update or RPC check")
		}
		b, err := io.ReadAll(io.LimitReader(in, 2049))
		if err != nil || len(b) > 2048 {
			return errors.New("invalid RPC check input")
		}
		var input core.RPCCheckRequest
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid RPC check input; no credentials accepted")
		}
		var result core.RPCCheckResult
		if err = c.Do(ctx, http.MethodPost, "/api/v1/accounts/rpc-check", input, &result); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	var result core.AccountsView
	if *update {
		b, readErr := io.ReadAll(io.LimitReader(in, 16385))
		if readErr != nil {
			return readErr
		}
		if len(b) > 16384 {
			return errors.New("oversized public account record")
		}
		var input core.AccountsRecord
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid public account record; credentials and approval flags are forbidden")
		}
		e = c.Do(ctx, http.MethodPut, "/api/v1/accounts", input, &result)
	} else {
		e = c.Do(ctx, http.MethodGet, "/api/v1/accounts", nil, &result)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}
