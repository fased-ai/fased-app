//go:build linux

package custodyinstall

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageWalletMetadataResumesWithoutReplacingForeignState(t *testing.T) {
	_, base := packageGateInitialRecord(t)
	target := base
	target.Network = "devnet"
	instance := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	files := packageWalletFiles(base, target, instance)
	marker, markerData := base.Root+"/wallet-configured.json", packageWalletMarker(target, instance)
	for stop := 1; stop <= len(files)+1; stop++ {
		t.Run(string(rune('0'+stop)), func(t *testing.T) {
			state := make(map[string][]byte)
			for _, file := range files {
				state[file.path] = bytes.Clone(file.old)
			}
			read := func(path string) (hostFile, error) {
				b, ok := state[path]
				if !ok {
					return hostFile{}, os.ErrNotExist
				}
				return hostFile{Data: bytes.Clone(b), UID: 0, Mode: 0644, Regular: true}, nil
			}
			step := 0
			interrupted := true
			write := func(path string, data []byte) error {
				state[path] = bytes.Clone(data)
				step++
				if interrupted && step == stop {
					return errors.New("interrupted after publication")
				}
				return nil
			}
			replace := func(path string, data []byte, _ os.FileMode, previous string) error {
				if sum(state[path]) == sum(data) {
					return nil
				}
				if sum(state[path]) != previous {
					return errors.New("foreign metadata")
				}
				return write(path, data)
			}
			publish := func(path string, data []byte, _ os.FileMode) error {
				if old, ok := state[path]; ok {
					if bytes.Equal(old, data) {
						return nil
					}
					return errors.New("foreign marker")
				}
				return write(path, data)
			}
			if err := inspectPackageWalletFiles(files, marker, markerData, read); err != nil {
				t.Fatal(err)
			}
			if err := publishPackageWalletFiles(files, marker, markerData, replace, publish); err == nil {
				t.Fatal("interruption was not reached")
			}
			interrupted = false
			if err := inspectPackageWalletFiles(files, marker, markerData, read); err != nil {
				t.Fatal("original transition could not resume", err)
			}
			if err := publishPackageWalletFiles(files, marker, markerData, replace, publish); err != nil {
				t.Fatal("original transition could not finish", err)
			}
			for _, file := range files {
				if !bytes.Equal(state[file.path], file.target) {
					t.Fatal("wrong configured file", file.path)
				}
			}
			state[files[0].path] = []byte("foreign")
			if err := inspectPackageWalletFiles(files, marker, markerData, read); err == nil || string(state[files[0].path]) != "foreign" {
				t.Fatal("foreign metadata was not preserved")
			}
		})
	}
}

func TestPackageWalletInitializationKeepsOriginalJournal(t *testing.T) {
	r, original := packageGateInitialRecord(t)
	request := update.WalletInitialization{Version: 1, OperationID: update.NewProbe(), InstanceID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), Network: "devnet"}
	r.Phase, r.Start, r.Wallet = "wallet-initializing", "", &request
	base, target, err := ValidatePackageWalletInitialization(r, original.Owner, original.Location, original.ID)
	if err != nil || base != original || target.Network != request.Network {
		t.Fatal("original wallet configuration could not be resumed", err)
	}
	if _, err = packageStartSelection(r, original.Owner, original.Location, original.ID, nil); err == nil {
		t.Fatal("a service could start before wallet initialization completed")
	}
	if r.RecoveryEligible() || r.ValidateIdle() == nil {
		t.Fatal("wallet initialization was mistaken for an idle or update journal")
	}
	for name, change := range map[string]func(*update.MaintenanceRecord){
		"other owner":    func(v *update.MaintenanceRecord) { v.Owner++ },
		"update hold":    func(v *update.MaintenanceRecord) { v.Installation = &update.Installation{} },
		"changed target": func(v *update.MaintenanceRecord) { v.Target = mustJSON(target) },
		"unsupported network": func(v *update.MaintenanceRecord) {
			w := *v.Wallet
			w.Network = "testnet"
			v.Wallet = &w
		},
		"invalid workspace": func(v *update.MaintenanceRecord) {
			w := *v.Wallet
			w.InstanceID = "other"
			v.Wallet = &w
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			change(&bad)
			if _, _, err := ValidatePackageWalletInitialization(bad, original.Owner, original.Location, original.ID); err == nil {
				t.Fatal("a foreign or held operation was accepted")
			}
		})
	}
}

func TestPackageSignerServiceDoesNotChangeWhenWalletIsConfigured(t *testing.T) {
	_, before := packageGateInitialRecord(t)
	after := before
	after.Network = "devnet"
	initial := definitions(before)["fased-signer.service"]
	configured := definitions(after)["fased-signer.service"]
	if !bytes.Equal(initial, configured) || !bytes.Contains(initial, []byte("ConditionPathExists="+before.Root+"/wallet-configured.json\n")) {
		t.Fatal("wallet setup would require changing the installed signer unit")
	}
}

func TestConfiguredPackageWalletUsesOriginalIdleJournal(t *testing.T) {
	for _, network := range []string{"devnet", "mainnet-beta"} {
		t.Run(network, func(t *testing.T) {
			r, original := packageGateInitialRecord(t)
			configured := original
			configured.Network = network
			r.Base, r.Target = mustJSON(configured), mustJSON(configured)
			selected, err := packageStartSelection(r, original.Owner, original.Location, original.ID, nil)
			if err != nil || selected != configured {
				t.Fatalf("configured wallet could not retain its installed journal: %v", err)
			}
			for name, change := range map[string]func(*update.MaintenanceRecord){
				"foreign owner": func(v *update.MaintenanceRecord) { v.Owner++ },
				"new operation": func(v *update.MaintenanceRecord) { v.OperationID = strings.Repeat("f", 32) },
				"different target": func(v *update.MaintenanceRecord) {
					m := configured
					m.Network = ""
					v.Target = mustJSON(m)
				},
				"unsupported network": func(v *update.MaintenanceRecord) {
					m := configured
					m.Network = "testnet"
					v.Base, v.Target = mustJSON(m), mustJSON(m)
				},
			} {
				t.Run(name, func(t *testing.T) {
					bad := r
					change(&bad)
					if _, err := packageStartSelection(bad, original.Owner, original.Location, original.ID, nil); err == nil {
						t.Fatal("a different package or journal was accepted")
					}
				})
			}
		})
	}
}
