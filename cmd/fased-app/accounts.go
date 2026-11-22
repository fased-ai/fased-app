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
	"github.com/fased-ai/fased-app/internal/desktop"
)

var accountsRemoteClient = connectedRemoteClient

func accountsCommand(args []string, in *os.File, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("accounts", flag.ContinueOnError)
	flags.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := flags.String("data-dir", base, "private instance directory")
	remote := flags.Bool("remote", false, "use the connected Tailscale instance")
	venueSecret := flags.Bool("venue-credential-stdin", false, "manage encrypted Jupiter/RPC connection lease from private JSON input")
	venueBackupOut := flags.String("venue-backup-out", "", "new private encrypted Jupiter backup file")
	exportPortable := flags.Bool("export-portable", false, "export portable public wallet and RPC configuration JSON; no secrets")
	importPortable := flags.Bool("import-portable-stdin", false, "import portable public wallet and RPC configuration JSON from stdin; no secrets")
	backupOut := flags.String("rpc-backup-out", "", "new private encrypted RPC backup file")
	rpcSecret := flags.Bool("rpc-credential-stdin", false, "manage disposable RPC credentials from private JSON input")
	rpcConfig := flags.Bool("rpc-stdin", false, "save/remove disposable wallet RPC settings from JSON")
	rpcCheck := flags.Bool("check-rpc-stdin", false, "check disposable RPC endpoint/network JSON; no saving or live calls")
	walletRead := flags.Bool("read-stdin", false, "read wallet balances/activity or selected transaction using private credential JSON")
	update := flags.Bool("request-stdin", false, "revision-checked public account record JSON from stdin; no secrets")
	setup := flags.Bool("setup-stdin", false, "add a network public address or verified signer reference from JSON; no secrets")
	if e = flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected account arguments")
	}
	if *remote && (*venueSecret || *venueBackupOut != "" || *importPortable || *backupOut != "" || *rpcSecret || *rpcConfig || *rpcCheck || *update || *setup) {
		return errors.New("connected Remote accounts currently support listing and read-only wallet observations")
	}
	if *exportPortable && (*walletRead || *venueSecret || *venueBackupOut != "" || *importPortable || *rpcSecret || *rpcConfig || *rpcCheck || *update || *setup || *backupOut != "") {
		return errors.New("choose one account operation")
	}
	if *importPortable && (*walletRead || *venueSecret || *venueBackupOut != "" || *rpcSecret || *rpcConfig || *rpcCheck || *update || *setup || *backupOut != "") {
		return errors.New("choose one account operation")
	}
	if *walletRead && (*venueSecret || *venueBackupOut != "" || *rpcSecret || *rpcConfig || *rpcCheck || *update || *setup || *backupOut != "") {
		return errors.New("choose one account operation")
	}
	if *backupOut != "" && !*rpcSecret {
		return errors.New("backup output requires RPC credential input")
	}
	if *venueBackupOut != "" && !*venueSecret {
		return errors.New("backup output requires venue credential input")
	}
	if *venueSecret && (*rpcSecret || *rpcConfig || *rpcCheck || *update || *setup || *backupOut != "") {
		return errors.New("choose one account operation")
	}
	ctx := context.Background()
	var c *core.Client
	if *remote {
		c, e = accountsRemoteClient(ctx, *dir)
	} else {
		var enrolled bool
		c, enrolled, e = desktop.NewPublicClient(ctx, *dir)
		if e == nil {
			if enrolled {
				// The exact OS-owner helper authenticates the native public path.
				e = c.NativeLocalOwner(ctx)
			} else {
				e = loadAccountsSession(ctx, c, *dir)
			}
		}
	}
	if e != nil {
		if c != nil {
			c.Close()
		}
		return e
	}
	defer c.Close()
	if *walletRead {
		var input core.WalletReadCommand
		b, err := io.ReadAll(io.LimitReader(in, 4097))
		defer clear(b)
		if err != nil || len(b) > 4096 {
			return errors.New("invalid wallet read input")
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid wallet read input")
		}
		var result core.WalletSnapshot
		if err = c.Do(ctx, http.MethodPost, "/api/v1/accounts/read", input, &result); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *exportPortable {
		var bundle core.PortableWalletsBundle
		if err := c.Do(ctx, http.MethodGet, "/api/v1/accounts/portable", nil, &bundle); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(bundle)
	}
	if *importPortable {
		b, err := io.ReadAll(io.LimitReader(in, 16385))
		if err != nil || len(b) > 16384 {
			return errors.New("invalid portable wallet bundle")
		}
		var input core.PortableWalletsImportCommand
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid portable wallet bundle")
		}
		var result core.PortableWalletsImportResult
		if err = c.Do(ctx, http.MethodPut, "/api/v1/accounts/portable", input, &result); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(result)
	}
	if *venueSecret {
		b, err := io.ReadAll(io.LimitReader(in, 4097))
		defer clear(b)
		if err != nil || len(b) > 4096 {
			return errors.New("invalid connection key input")
		}
		var input core.VenueCredentialCommand
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid connection key input")
		}
		if (input.Action == "backup") != (*venueBackupOut != "") {
			return errors.New("backup requires a new private output file")
		}
		var result core.VenueCredentialView
		if err = c.Do(ctx, http.MethodPut, "/api/v1/accounts/venue-credential", input, &result); err != nil {
			return err
		}
		if input.Action == "backup" {
			if err = core.WriteVenueBackup(*venueBackupOut, result.Backup); err != nil {
				return err
			}
			result.Backup = nil
		}
		return json.NewEncoder(out).Encode(result)
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
