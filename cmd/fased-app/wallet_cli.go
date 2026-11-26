package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"golang.org/x/term"
)

func readCLIRecoveryPassphrase(in *os.File, errOut io.Writer, confirm bool) (string, error) {
	if in != nil && term.IsTerminal(int(in.Fd())) {
		_, _ = fmt.Fprint(errOut, "Recovery passphrase (min 12 chars): ")
		p1, err := term.ReadPassword(int(in.Fd()))
		_, _ = fmt.Fprintln(errOut)
		if err != nil {
			return "", err
		}
		s1 := string(p1)
		clear(p1)
		if len(strings.TrimSpace(s1)) < 12 {
			return "", errors.New("passphrase must be at least 12 characters")
		}
		if !confirm {
			return s1, nil
		}
		_, _ = fmt.Fprint(errOut, "Confirm recovery passphrase: ")
		p2, err := term.ReadPassword(int(in.Fd()))
		_, _ = fmt.Fprintln(errOut)
		if err != nil {
			return "", err
		}
		s2 := string(p2)
		clear(p2)
		if s1 != s2 {
			return "", errors.New("passphrases do not match")
		}
		return s1, nil
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	pass := strings.TrimRight(line, "\r\n")
	if len(strings.TrimSpace(pass)) < 12 {
		return "", errors.New("passphrase must be at least 12 characters")
	}
	return pass, nil
}

func openCLIWalletBackend(ctx context.Context, dataDir, explicitSocket string) (custody.Backend, *custody.SocketClient, *custody.Service, error) {
	sock := strings.TrimSpace(explicitSocket)
	if sock == "" {
		candidate := filepath.Join(dataDir, "signer-vault", "owner.sock")
		if ok, _ := custody.ProbeSocketAt(ctx, candidate); ok {
			sock = candidate
		}
	}
	if sock != "" {
		c, err := custody.NewSocketClient(sock)
		if err != nil {
			return nil, nil, nil, err
		}
		return c, c, nil, nil
	}
	svc, err := custody.NewService(custody.Config{DataDir: dataDir})
	if err != nil {
		return nil, nil, nil, err
	}
	return svc, nil, svc, nil
}

func walletCommand(args []string, in *os.File, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: fased-app wallet status|create|import|recovery|rpc [options]")
	}
	defDir, err := defaultDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "status":
		fs := flag.NewFlagSet("wallet status", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dir := fs.String("data-dir", defDir, "state directory")
		sock := fs.String("socket", "", "optional fased-signer Unix socket path")
		asJSON := fs.Bool("json", true, "emit JSON")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		be, sc, _, err := openCLIWalletBackend(ctx, *dir, *sock)
		if err != nil {
			return err
		}
		defer be.Close()
		list, err := be.ListWallets(ctx)
		if err != nil {
			return err
		}
		sockReady := sc != nil || custody.ProbeSocket(ctx, *dir)
		if *asJSON {
			return json.NewEncoder(out).Encode(map[string]any{
				"wallets":     list,
				"count":       len(list),
				"socketReady": sockReady,
			})
		}
		for _, w := range list {
			_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", w.Handle, w.Role, w.Custody, w.PublicKey, w.RPC.Cluster)
		}
		return nil

	case "create":
		fs := flag.NewFlagSet("wallet create", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dir := fs.String("data-dir", defDir, "state directory")
		sock := fs.String("socket", "", "optional fased-signer Unix socket path")
		name := fs.String("name", "", "wallet display name")
		role := fs.String("role", "", "wallet role: agent, trading, vault, mining")
		rpcURL := fs.String("rpc-url", "", "Solana RPC URL")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		be, _, _, err := openCLIWalletBackend(ctx, *dir, *sock)
		if err != nil {
			return err
		}
		defer be.Close()
		w, err := be.CreateNativeWallet(ctx, *name, *role, *rpcURL)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(w)

	case "import":
		fs := flag.NewFlagSet("wallet import", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dir := fs.String("data-dir", defDir, "state directory")
		sock := fs.String("socket", "", "optional fased-signer owner Unix socket path")
		name := fs.String("name", "", "wallet display name")
		role := fs.String("role", "", "wallet role: agent, trading, vault, mining")
		filePath := fs.String("file", "", "absolute path to 0600 Solana JSON keypair file")
		rpcURL := fs.String("rpc-url", "", "Solana RPC URL")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		be, sc, svc, err := openCLIWalletBackend(ctx, *dir, *sock)
		if err != nil {
			return err
		}
		defer be.Close()
		var w custody.WalletPublic
		if sc != nil {
			w, err = sc.ImportSolanaKeypairFile(ctx, *filePath, *name, *role, *rpcURL)
		} else {
			w, err = svc.ImportSolanaKeypairFile(ctx, *filePath, *name, *role, *rpcURL)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(w)

	case "rpc":
		if len(rest) == 0 || rest[0] != "set" {
			return errors.New("usage: fased-app wallet rpc set --wallet-id <id> --rpc-url <url>")
		}
		fs := flag.NewFlagSet("wallet rpc set", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dir := fs.String("data-dir", defDir, "state directory")
		sock := fs.String("socket", "", "optional fased-signer Unix socket path")
		walletID := fs.String("wallet-id", "", "wallet id or @wallet:<id>")
		rpcURL := fs.String("rpc-url", "", "primary Solana RPC URL")
		fallbackURL := fs.String("fallback-rpc-url", "", "optional distinct fallback RPC URL")
		if err := fs.Parse(rest[1:]); err != nil {
			return err
		}
		be, _, _, err := openCLIWalletBackend(ctx, *dir, *sock)
		if err != nil {
			return err
		}
		defer be.Close()
		w, err := be.SetWalletRPC(ctx, *walletID, *rpcURL, *fallbackURL, "owner-cli")
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(w)

	case "recovery":
		if len(rest) == 0 {
			return errors.New("usage: fased-app wallet recovery export|import|export-raw [options]")
		}
		action := rest[0]
		subRest := rest[1:]
		switch action {
		case "export":
			fs := flag.NewFlagSet("wallet recovery export", flag.ContinueOnError)
			fs.SetOutput(errOut)
			dir := fs.String("data-dir", defDir, "state directory")
			sock := fs.String("socket", "", "optional fased-signer owner Unix socket path")
			walletID := fs.String("wallet-id", "", "wallet id")
			output := fs.String("output", "", "new clean absolute output path")
			if err := fs.Parse(subRest); err != nil {
				return err
			}
			cleanOut := strings.TrimSpace(*output)
			if !filepath.IsAbs(cleanOut) || filepath.Clean(cleanOut) != cleanOut {
				return errors.New("--output must be a clean absolute path")
			}
			if _, err := os.Lstat(cleanOut); err == nil {
				return errors.New("output file already exists; choose a new path")
			}
			pass, err := readCLIRecoveryPassphrase(in, errOut, true)
			if err != nil {
				return err
			}
			be, _, _, err := openCLIWalletBackend(ctx, *dir, *sock)
			if err != nil {
				return err
			}
			defer be.Close()
			archive, fp, err := be.ExportEncryptedRecovery(ctx, *walletID, pass, "owner-cli")
			if err != nil {
				return err
			}
			f, err := os.OpenFile(cleanOut, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, wErr := f.Write(append(bytes.Clone(archive), '\n')); wErr != nil {
				_ = f.Close()
				_ = os.Remove(cleanOut)
				return wErr
			}
			_ = f.Sync()
			_ = f.Close()
			return json.NewEncoder(out).Encode(map[string]any{"output": cleanOut, "fingerprint": fp})

		case "import":
			fs := flag.NewFlagSet("wallet recovery import", flag.ContinueOnError)
			fs.SetOutput(errOut)
			dir := fs.String("data-dir", defDir, "state directory")
			sock := fs.String("socket", "", "optional fased-signer owner Unix socket path")
			filePath := fs.String("file", "", "encrypted recovery file path")
			name := fs.String("name", "", "wallet display name")
			role := fs.String("role", "", "optional role override")
			rpcURL := fs.String("rpc-url", "", "Solana RPC URL")
			if err := fs.Parse(subRest); err != nil {
				return err
			}
			raw, err := os.ReadFile(strings.TrimSpace(*filePath))
			if err != nil {
				return err
			}
			pass, err := readCLIRecoveryPassphrase(in, errOut, false)
			if err != nil {
				return err
			}
			be, _, _, err := openCLIWalletBackend(ctx, *dir, *sock)
			if err != nil {
				return err
			}
			defer be.Close()
			w, err := be.ImportEncryptedRecovery(ctx, raw, pass, *name, *role, *rpcURL)
			if err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(w)

		case "export-raw":
			fs := flag.NewFlagSet("wallet recovery export-raw", flag.ContinueOnError)
			fs.SetOutput(errOut)
			dir := fs.String("data-dir", defDir, "state directory")
			sock := fs.String("socket", "", "optional fased-signer owner Unix socket path")
			walletID := fs.String("wallet-id", "", "wallet id")
			output := fs.String("output", "", "new clean absolute output path")
			ack := fs.Bool("acknowledge-custody-reduction", false, "explicitly acknowledge raw key export outside signer custody")
			if err := fs.Parse(subRest); err != nil {
				return err
			}
			be, sc, svc, err := openCLIWalletBackend(ctx, *dir, *sock)
			if err != nil {
				return err
			}
			defer be.Close()
			if sc != nil {
				err = sc.ExportRawKeypairFile(ctx, *walletID, *output, *ack)
			} else {
				err = svc.ExportRawKeypairFile(ctx, *walletID, *output, *ack)
			}
			if err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(map[string]any{"output": *output, "custodyReduced": true})

		default:
			return fmt.Errorf("unknown wallet recovery action %q", action)
		}

	default:
		return fmt.Errorf("unknown wallet subcommand %q", sub)
	}
}
