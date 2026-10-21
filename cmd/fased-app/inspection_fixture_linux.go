//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/term"
	"io"
	"os"
	"os/exec"
)

func inspectionFixtureCommand(args []string, in *os.File, out, errOut io.Writer) error {
	if len(args) == 1 && args[0] == "fixture-inspection-compatibility" {
		return json.NewEncoder(out).Encode(update.FixtureInspectionContract())
	}
	if len(args) == 0 {
		return errors.New("explicit fixture action required")
	}
	op := args[0]
	f := flag.NewFlagSet(op, flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", os.Getuid(), "fixed installed owner")
	history := f.Bool("inspection-status", false, "historical read-only status")
	fixture := f.Bool("disposable-fixture", false, "disposable installed fixture only")
	prepare := f.Bool("prepare-host", false, "fixed OS publication consent")
	id := f.String("bridge-id", "", "exact immutable fixture bridge")
	txpath := f.String("transaction", "", "public disposable bootstrap capsule")
	inputs := f.String("input-dir", "", "public sealed fixture inputs")
	artifacts := f.String("artifact-dir", "", "exact fixture readers")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 || !*fixture || *owner <= 0 {
		return errors.New("explicit disposable fixture and owner required")
	}
	root := custodyinstall.Root(*owner)
	ctx := context.Background()
	switch op {
	case "fixture-reader-guard":
		if _, e := update.VerifyInstalledInspectionGuard(root, "core"); e != nil {
			return e
		}
		m, e := custodyinstall.Load(*owner)
		if e != nil {
			return e
		}
		return run([]string{"serve", "--data-dir", root + "/app-state", "--listen", m.Listen, "--require-sandbox", "--installation-root", root, "--custody-config", root + "/identity.json"}, in, out, errOut)
	case "fixture-inspection-cold":
		cfg, e := signer.LoadConfig(root + "/identity.json")
		if e != nil {
			return e
		}
		if os.Getuid() != cfg.AppUID || os.Geteuid() != cfg.AppUID {
			return errors.New("cold metadata requires app UID")
		}
		if e = signer.VerifyExecutable(); e != nil {
			return e
		}
		v, e := core.InspectFixtureColdMetadata(ctx, root+"/app-state", update.NewProbe())
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	case "fixture-inspection-empty":
		cfg, e := signer.LoadConfig(root + "/identity.json")
		if e != nil {
			return e
		}
		if e = signer.CheckOwner(cfg); e != nil {
			return e
		}
		empty, e := update.FixtureInspectionNamespaceEmpty(root, "owner")
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]bool{"empty": empty})
	case "fixture-inspection-init":
		return update.InitializeInstalledInspectionFixture(ctx, root, "owner")
	}
	if os.Getuid() != *owner || os.Geteuid() != *owner {
		return errors.New("protected owner identity required")
	}
	system := &custodyinstall.System{In: in, Out: errOut}
	if op == "provision" {
		if !*prepare || !term.IsTerminal(int(in.Fd())) {
			return errors.New("protected owner terminal and OS consent required")
		}
		b, e := readInspectionInput(*txpath)
		if e != nil {
			return e
		}
		var tx custodyinstall.InspectionBridge
		if update.StrictJSON(b, &tx) != nil || tx.Base.Owner != *owner {
			return errors.New("invalid fixture capsule")
		}
		if _, e = tx.Plan(); e != nil {
			return e
		}
		self, e := os.Executable()
		if e != nil {
			return e
		}
		image, e := os.ReadFile(self)
		if e != nil || fileSHA(image) != tx.Target.CoreHash {
			return errors.New("run the independently pinned candidate core")
		}
		want := "BRIDGE FIXTURE " + tx.Digest()
		pretty, _ := json.MarshalIndent(tx, "", "  ")
		fmt.Fprintf(errOut, "DISPOSABLE GUARDED READER BRIDGE\nNo financial install, unlock or automatic update. Reserve maintenance, retain histories and a forward-only fence.\n%s\nType %s: ", pretty, want)
		line, e := bufio.NewReader(in).ReadString('\n')
		if e != nil || line != want+"\n" {
			return errors.New("bridge review cancelled")
		}
		lock, e := custodyinstall.OwnerLock()
		if e != nil {
			return e
		}
		e = custodyinstall.PrepareInspectionBridge(ctx, tx, *inputs, *artifacts, system)
		lock.Close()
		if e != nil {
			return e
		}
		child := exec.Command(tx.Dir()+"/candidate/fased-app", "owner-fixture-inspection", "--bridge-id", tx.ID, "--disposable-fixture", "--prepare-host")
		child.Stdin = in
		child.Stdout = out
		child.Stderr = errOut
		return child.Run()
	}
	if op == "bridge" || op == "rollback" || op == "owner-fixture-inspection" && *id != "" {
		if !*prepare {
			return errors.New("fixed OS consent required")
		}
		tx, e := custodyinstall.LoadInspectionBridge(*owner, *id)
		if e != nil {
			return e
		}
		selfPath, se := os.Executable()
		if se != nil {
			return se
		}
		if selfPath != tx.Dir()+"/candidate/fased-app" && selfPath != root+"/fased-app" {
			child := exec.Command(tx.Dir()+"/candidate/fased-app", "custody", "fixture-inspection", op, "--bridge-id", tx.ID, "--disposable-fixture", "--prepare-host")
			child.Stdin = in
			child.Stdout = out
			child.Stderr = errOut
			return child.Run()
		}

		lock, e := custodyinstall.OwnerLock()
		if e != nil {
			return e
		}
		defer lock.Close()
		cfg, e := signer.LoadConfig(root + "/identity.json")
		if e != nil {
			return e
		}
		if e = signer.CheckOwner(cfg); e != nil {
			return e
		}
		self, e := os.Executable()
		if e != nil {
			return e
		}
		b, e := os.ReadFile(self)
		if e != nil || fileSHA(b) != tx.Target.CoreHash {
			return errors.New("exact immutable candidate recovery helper required")
		}
		carrier := custodyinstall.BootstrapTransaction{ID: tx.ID, Base: tx.Base, Target: tx.Target, Core: tx.Core, Signer: tx.Signer, Trust: tx.Trust, Bundle: update.BootstrapBundle{Maintenance: tx.Maintenance}}
		callbacks := maintenanceCallbacks(carrier, system, cfg, tx.Dir(), tx.Plan, tx.Current)
		var v custodyinstall.BootstrapReceipt
		if op == "rollback" {
			v, e = custodyinstall.RollbackInspectionBridge(ctx, tx, system, callbacks)
		} else {
			v, e = custodyinstall.RunInspectionBridge(ctx, tx, system, callbacks)
		}
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	if op == "inspect" || op == "status" || op == "owner-fixture-inspection" {
		if op == "status" {
			*history = true
		}
		if !*prepare {
			return errors.New("owner metadata OS probe consent required")
		}
		self, e := os.Executable()
		if e != nil {
			return e
		}
		if self != root+"/fased-app" {
			childArgs := []string{"owner-fixture-inspection", "--disposable-fixture", "--prepare-host"}
			if *history {
				childArgs = append(childArgs, "--inspection-status")
			}
			child := exec.Command(root+"/fased-app", childArgs...)
			child.Stdin = in
			child.Stdout = out
			child.Stderr = errOut
			return child.Run()
		}
		if !*history && !term.IsTerminal(int(in.Fd())) {
			return errors.New("protected inspection terminal required")
		}
		role, e := update.OpenInstalledInspectionFixture(root, "owner")
		if e != nil {
			return e
		}
		defer role.Close()
		if *history {
			v, e := update.InstalledFixtureStatus(ctx, role)
			if e != nil {
				return e
			}
			return json.NewEncoder(out).Encode(v)
		}
		probe := func(ctx context.Context, p string) (update.CoreSnapshot, error) {
			m, e := custodyinstall.Load(*owner)
			if e != nil {
				return update.CoreSnapshot{}, e
			}
			var b bytes.Buffer
			if e = system.UpdateInventory(ctx, m, p, &b); e != nil {
				return update.CoreSnapshot{}, e
			}
			var c update.CoreSnapshot
			e = update.StrictJSON(b.Bytes(), &c)
			return c, e
		}
		review := func(want string, card update.InstalledInspectionStatus) error {
			b, _ := json.MarshalIndent(card, "", "  ")
			fmt.Fprintf(errOut, "INSPECTION ONLY: all execution capabilities remain disabled\n%s\nType %s: ", b, want)
			line, e := bufio.NewReader(in).ReadString('\n')
			if e != nil || line != want+"\n" {
				return errors.New("inspection cancelled")
			}
			return nil
		}
		v, e := update.InspectInstalledFixture(ctx, role, probe, review)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	return errors.New("unknown fixture inspection action")
}

func readInspectionInput(p string) ([]byte, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, update.MaxDocument+1))
	if e != nil || len(b) > update.MaxDocument {
		return nil, errors.New("bounded public fixture capsule required")
	}
	return b, nil
}
