// Development-only executable. Excluded from ordinary packaging/services.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

func emit(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
func run(ctx context.Context, args []string) error {
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() || runtime.GOOS != "linux" || len(args) < 2 {
		return errors.New("ordinary-user Linux fixture command required")
	}
	role, action := args[0], args[1]
	f := flag.NewFlagSet("fixture", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	fixture := f.Bool("disposable-fixture", false, "")
	config := f.String("config", "", "")
	pin := f.String("config-sha256", "", "")
	initial := f.Bool("initial", false, "")
	id := f.String("request-id", "", "")
	exchange := f.String("exchange-digest", "", "")
	if f.Parse(args[2:]) != nil || f.NArg() != 0 || !*fixture || *config == "" || len(*pin) != 64 {
		return errors.New("explicit pinned disposable fixture options required")
	}
	if role == "probe" {
		if *initial || *id != "" || *exchange != "" {
			return errors.New("probe options refused")
		}
		return probe(ctx, action, *config, *pin)
	}
	if role != "owner" && role != "signer" || role == "owner" && action != "inspect" && action != "status" || role == "signer" && action != "serve" || action != "status" && (*id != "" || *exchange != "") || action == "status" && *initial {
		return errors.New("unknown fixture operation")
	}
	r, e := update.LoadFixtureTransportRole(*config, *pin, update.FixtureTransportLoadOptions{Initial: *initial, Audit: action == "status"})
	if e != nil {
		return e
	}
	defer r.Close()
	// A role mismatch is checked in the API, never selected by an RPC message.
	if role == "signer" {
		s, e := update.NewFixtureSignerServer(r)
		if e != nil {
			return e
		}
		defer s.Close()
		emit(map[string]any{"event": "ready", "pid": os.Getpid(), "uid": os.Getuid()})
		return s.Serve(ctx)
	}
	client, e := update.NewFixtureOwnerClient(r, func(review update.FixtureTransportReview) bool {
		return update.FixtureFileReview(ctx, r, review, func(v update.FixtureTransportReview) {
			emit(map[string]any{"event": "review", "review": v, "pid": os.Getpid(), "uid": os.Getuid()})
		})
	})
	if e != nil {
		return e
	}
	if action == "status" {
		v, e := client.Audit(ctx, *id, *exchange)
		if e != nil {
			return e
		}
		emit(map[string]any{"event": "audit", "historical": true, "record": json.RawMessage(v)})
		return nil
	}
	out, e := client.Inspect(ctx)
	if e != nil {
		return e
	}
	emit(map[string]any{"event": "complete", "report": out, "pid": os.Getpid(), "uid": os.Getuid()})
	return nil
}
func probe(ctx context.Context, action, path, pin string) error {
	// Probe configuration is private to the untrusted fixture UID. It has no actor,
	// trust, clock, review, inventory, store or lifecycle inputs.
	c, e := update.LoadFixtureProbeConfig(path, pin)
	if e != nil {
		return e
	}
	if action == "fake-signer" {
		l, e := net.ListenUnix("unix", &net.UnixAddr{Name: c.Socket, Net: "unix"})
		if e != nil {
			return e
		}
		l.SetUnlinkOnClose(false)
		defer l.Close()
		if e = os.Chmod(c.Socket, 0666); e != nil {
			return e
		}
		stop := context.AfterFunc(ctx, func() { _ = l.Close() })
		defer stop()
		emit(map[string]any{"event": "ready", "pid": os.Getpid(), "uid": os.Getuid()})
		conn, e := l.AcceptUnix()
		if e != nil {
			return e
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var p [1]byte
		n, e := conn.Read(p[:])
		if n != 0 {
			return errors.New("owner sent data to forged peer")
		}
		emit(map[string]any{"event": "fake-peer-denied", "bytes": n, "uid": os.Getuid()})
		return nil
	}
	if action != "raw" {
		return errors.New("unknown probe")
	}
	payload, e := io.ReadAll(io.LimitReader(os.Stdin, update.MaxDocument+5))
	if e != nil || len(payload) > update.MaxDocument+4 {
		return errors.New("probe bound")
	}
	v, e := update.FixtureTransportProbe(ctx, c.Socket, c.SignerUID, payload)
	if e != nil {
		return e
	}
	emit(map[string]any{"event": "probe", "bytes": len(v), "uid": os.Getuid()})
	return nil
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e := run(ctx, os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "fixture operation refused:", e)
		os.Exit(1)
	}
}
