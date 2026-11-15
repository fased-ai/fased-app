//go:build linux

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPackageCandidateStageRefusesUnverifiedInputBeforeHelper(t *testing.T) {
	old := packageCandidateCommandForStage
	defer func() { packageCandidateCommandForStage = old }()
	called := false
	packageCandidateCommandForStage = func(context.Context, string, string, maintenance.CandidateRequest, io.Writer) (*exec.Cmd, []byte, error) {
		called = true
		return nil, nil, nil
	}
	if err := stageAttestedPackageCandidate(t.Context(), &bootstrap.Prepared{}, strings.Repeat("a", 64), "local", strings.Repeat("1", 32), strings.Repeat("b", 64), io.Discard); err == nil || called {
		t.Fatal("unverified native input reached the protected receiver", err)
	}
}

// Run by the existing signed native-candidate fixture. The sink is an ordinary
// subprocess and does not impersonate or execute the installed root helper.
func TestPackageCandidateStreamCLI(t *testing.T) {
	dir := os.Getenv("FASED_ATTESTED_CANDIDATE_FIXTURE")
	if dir == "" || os.Getenv("FASED_NATIVE_PACKAGE_CANDIDATE") != "1" {
		t.Skip("signed native candidate fixture not supplied")
	}
	seed, err := os.ReadFile(filepath.Join(dir, "first-install-seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	seedHash := sha256.Sum256(seed)
	packet, err := os.ReadFile(filepath.Join(dir, bootstrap.AttestedPacketFile))
	if err != nil {
		t.Fatal(err)
	}
	packetHash := sha256.Sum256(packet)
	packetSHA := hex.EncodeToString(packetHash[:])
	var signed bootstrap.AttestedPacket
	if update.StrictJSON(packet, &signed) != nil {
		t.Fatal("invalid signed candidate packet")
	}
	baseline, instance := strings.Repeat("4", 64), strings.Repeat("3", 32)
	parent := t.TempDir()
	if err = os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	opts := bootstrap.Options{Seed: seed, SeedSHA256: hex.EncodeToString(seedHash[:]), Directory: filepath.Join(parent, "held")}
	admission := bootstrap.CandidateAdmission{PacketSHA256: packetSHA, OperationID: strings.Repeat("1", 32), BaselineSHA256: baseline, ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	p, err := bootstrap.PrepareAttestedCandidate(t.Context(), opts, dir, admission)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	report := p.Report()
	if report.Sequence < 2 {
		t.Fatal("fixture must advance an installed release")
	}
	release, _, err := p.PackageApplicationRelease()
	if err != nil {
		t.Fatal("signed package release unavailable", err)
	}
	receipt := maintenance.CandidateReceipt{Version: 1, Owner: os.Getuid(), Scope: "update", PacketSHA256: packetSHA,
		InstallerSHA256: signed.Installer.SHA256, EngineSHA256: release.EngineSHA256, ProductVersion: release.Version,
		Sequence: report.Sequence, ReleaseSHA256: packetSHA, Target: report.Target,
		BaseSequence: report.Sequence - 1, BaseReleaseSHA256: baseline, BaseInstallation: instance,
		BaseManifestSHA: strings.Repeat("c", 64), ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err = receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	oldRead, oldCommand := readPackageCandidateForStage, packageCandidateCommandForStage
	defer func() { readPackageCandidateForStage, packageCandidateCommandForStage = oldRead, oldCommand }()
	oldBinding, oldBaseline, oldStage := compiledFirstInstallBinding, packageCandidateUpdateBaseline, packageCandidateMaintenanceStage
	defer func() {
		compiledFirstInstallBinding, packageCandidateUpdateBaseline, packageCandidateMaintenanceStage = oldBinding, oldBaseline, oldStage
	}()
	compiledFirstInstallBinding = &firstInstallBinding{Seed: seed, SHA256: hex.EncodeToString(seedHash[:])}
	packageCandidateUpdateBaseline = func(location, id string) (string, error) {
		if location != "local" || id != instance {
			return "", errors.New("wrong package instance")
		}
		return baseline, nil
	}
	packageCandidateMaintenanceStage = stageAttestedPackageCandidate
	t.Setenv("XDG_CACHE_HOME", filepath.Join(parent, "cache"))
	readPackageCandidateForStage = func(int, string, string) (maintenance.CandidateReceipt, error) {
		return maintenance.CandidateReceipt{}, os.ErrNotExist
	}
	badField, reuse, calls := "", false, 0
	packageCandidateCommandForStage = func(ctx context.Context, location, id string, q maintenance.CandidateRequest, diagnostics io.Writer) (*exec.Cmd, []byte, error) {
		calls++
		if location != "local" || id != instance || q.PacketSHA256 != packetSHA || q.Scope != "update" {
			return nil, nil, errors.New("wrong protected receiver")
		}
		sink := filepath.Join(t.TempDir(), "candidate")
		if err := os.Mkdir(sink, 0700); err != nil {
			return nil, nil, err
		}
		response := receipt
		switch badField {
		case "base":
			response.BaseReleaseSHA256 = strings.Repeat("9", 64)
		case "instance":
			response.BaseInstallation = strings.Repeat("8", 32)
		case "owner":
			response.Owner++
		case "engine":
			response.EngineSHA256 = strings.Repeat("9", 64)
		case "version":
			response.ProductVersion += "-different"
		case "installer":
			response.InstallerSHA256 = strings.Repeat("9", 64)
		}
		raw, err := json.Marshal(response)
		if err != nil {
			return nil, nil, err
		}
		request, _ := json.Marshal(maintenance.PackageRequest{Version: 1, Action: "candidate-stage", Candidate: &q})
		frame := append([]byte(maintenance.CandidateStreamPrefix), request...)
		frame = append(frame, '\n')
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPackageCandidateSink$")
		cmd.Env = append(os.Environ(), "FASED_PACKAGE_SINK=1", "FASED_PACKAGE_SINK_DIR="+sink,
			"FASED_PACKAGE_SINK_SEED="+filepath.Join(dir, "first-install-seed.json"), "FASED_PACKAGE_SINK_RECEIPT="+string(raw))
		if reuse {
			cmd.Env = append(cmd.Env, "FASED_PACKAGE_SINK_REUSE=1")
		}
		cmd.Stderr = diagnostics
		return cmd, frame, nil
	}
	args := []string{"--stage-candidate", dir, "--candidate-packet-sha256", packetSHA, "--package-location", "local", "--package-instance", instance}
	if err = run(args, strings.NewReader(""), io.Discard, io.Discard); err != nil || calls != 1 {
		t.Fatal("candidate CLI did not reach the exact scoped receiver", err)
	}
	for _, field := range []string{"base", "instance", "owner", "engine", "version", "installer"} {
		badField = field
		if err = stageAttestedPackageCandidate(t.Context(), p, packetSHA, "local", instance, baseline, io.Discard); err == nil {
			t.Fatal("foreign receiver response was accepted", field)
		}
	}
	badField, reuse = "", true
	readPackageCandidateForStage = func(int, string, string) (maintenance.CandidateReceipt, error) { return receipt, nil }
	if err = stageAttestedPackageCandidate(t.Context(), p, packetSHA, "local", instance, baseline, io.Discard); err != nil {
		t.Fatal("matching retained candidate did not reuse its receipt", err)
	}
	foreign := receipt
	foreign.BaseReleaseSHA256 = strings.Repeat("9", 64)
	readPackageCandidateForStage = func(int, string, string) (maintenance.CandidateReceipt, error) { return foreign, nil }
	before := calls
	if err = stageAttestedPackageCandidate(t.Context(), p, packetSHA, "local", instance, baseline, io.Discard); err == nil || calls != before {
		t.Fatal("foreign retained candidate reached the receiver", err)
	}
}

func TestPackageCandidateSink(t *testing.T) {
	if os.Getenv("FASED_PACKAGE_SINK") != "1" {
		t.Skip("subprocess sink only")
	}
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	r := bufio.NewReader(os.Stdin)
	prefix, err := r.ReadString('\n')
	if err != nil || prefix != maintenance.CandidateStreamPrefix {
		fail(errors.New("invalid candidate stream prefix"))
	}
	line, err := r.ReadBytes('\n')
	request, decodeErr := maintenance.DecodePackageRequest(line)
	if err != nil || decodeErr != nil || request.Action != "candidate-stage" || request.Candidate == nil {
		fail(errors.New("invalid package candidate request"))
	}
	if os.Getenv("FASED_PACKAGE_SINK_REUSE") == "1" {
		if b, e := io.ReadAll(io.LimitReader(r, 1)); e != nil || len(b) != 0 {
			fail(errors.New("reused candidate must not be streamed again"))
		}
	} else {
		destination := os.Getenv("FASED_PACKAGE_SINK_DIR")
		if err = bootstrap.ReceiveCandidate(context.Background(), r, destination, request.Candidate.PacketSHA256); err != nil {
			fail(err)
		}
		seed, e := os.ReadFile(os.Getenv("FASED_PACKAGE_SINK_SEED"))
		if e != nil {
			fail(e)
		}
		seedHash := sha256.Sum256(seed)
		if _, e = bootstrap.VerifyAttestedCandidate(bootstrap.Options{Seed: seed, SeedSHA256: hex.EncodeToString(seedHash[:])}, destination); e != nil {
			fail(e)
		}
	}
	var receipt maintenance.CandidateReceipt
	if update.StrictJSON([]byte(os.Getenv("FASED_PACKAGE_SINK_RECEIPT")), &receipt) != nil {
		fail(errors.New("invalid fixture receipt"))
	}
	payload, _ := json.Marshal(receipt)
	if err = json.NewEncoder(os.Stdout).Encode(maintenance.Response{Version: 1, Action: "candidate-stage", Complete: true, Payload: payload}); err != nil {
		fail(err)
	}
	os.Exit(0)
}
