//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestEnrollmentPrivateImageChannelHoldsSourceAndPreservesInstance(t *testing.T) {
	if os.Getuid() <= 0 {
		t.Skip("ordinary fixture owner required")
	}
	dir := privateEnrollmentDir(t)
	s, e := core.OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	input, ack := io.Pipe()
	output, private := io.Pipe()
	done := make(chan error, 1)
	defer input.Close()
	defer ack.Close()
	defer output.Close()
	defer private.Close()
	go func() {
		done <- enrollmentExportFixture(t.Context(), []string{"export", "--data-dir", dir, "--expected-instance", snap.InstanceID, "--disposable-fixture"}, input, private)
	}()
	var image core.EnrollmentImage
	if e = json.NewDecoder(output).Decode(&image); e != nil {
		t.Fatal(e)
	}
	if image.InstanceID != snap.InstanceID {
		t.Fatal("source identity changed")
	}
	if _, lock, e := core.ExportEnrollmentImage(t.Context(), dir); e == nil {
		lock.Close()
		t.Fatal("export lock released before fence acknowledgement")
	}
	digest := core.EnrollmentImageDigest(image)
	if _, e = fmt.Fprintln(ack, "CHECK "+digest); e != nil {
		t.Fatal(e)
	}
	proof := make([]byte, len("LOCKED "+digest+"\n"))
	if _, e = io.ReadFull(output, proof); e != nil || string(proof) != "LOCKED "+digest+"\n" {
		t.Fatal("source lease confirmation", e)
	}
	if _, e = fmt.Fprintln(ack, "RELEASE "+digest); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	var payload bytes.Buffer
	if e = json.NewEncoder(&payload).Encode(image); e != nil {
		t.Fatal(e)
	}
	stage := privateEnrollmentDir(t)
	var result bytes.Buffer
	args := []string{"import", "--data-dir", stage, "--expected-instance", snap.InstanceID, "--digest", digest, "--disposable-fixture"}
	if e = enrollmentImageCommand(t.Context(), args, &payload, &result, io.Discard); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(result.String(), "files") || !strings.Contains(result.String(), digest) {
		t.Fatal("import emitted private payload or omitted receipt")
	}
	result.Reset()
	args[0] = "verify"
	if e = enrollmentImageCommand(t.Context(), args, nil, &result, io.Discard); e != nil {
		t.Fatal(e)
	}
	restored, e := core.OpenStore(stage)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	actual, e := restored.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil || actual.InstanceID != snap.InstanceID {
		t.Fatal("import re-created the instance", e)
	}
}

func TestEnrollmentImageChannelRefusesWrongIdentityAndInput(t *testing.T) {
	if os.Getuid() <= 0 {
		t.Skip("ordinary fixture owner required")
	}
	dir := privateEnrollmentDir(t)
	s, e := core.OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	var out bytes.Buffer
	e = enrollmentImageCommand(context.Background(), []string{"export", "--data-dir", dir, "--expected-instance", "wrong-instance", "--disposable-fixture"}, strings.NewReader(""), &out, io.Discard)
	if e == nil || out.Len() != 0 {
		t.Fatal("wrong instance disclosed a private image", e)
	}
	image, lock, e := core.ExportEnrollmentImage(t.Context(), dir)
	if e != nil {
		t.Fatal(e)
	}
	lock.Close()
	raw, _ := json.Marshal(image)
	digest := core.EnrollmentImageDigest(image)
	for _, body := range []string{string(raw) + " {}", strings.TrimSuffix(string(raw), "}") + `,"seed":"disposable-not-a-wallet"}`} {
		stage := privateEnrollmentDir(t)
		out.Reset()
		e = enrollmentImageCommand(t.Context(), []string{"import", "--data-dir", stage, "--expected-instance", image.InstanceID, "--digest", digest, "--disposable-fixture"}, strings.NewReader(body), &out, io.Discard)
		files, _ := os.ReadDir(stage)
		if e == nil || len(files) != 0 || out.Len() != 0 {
			t.Fatal("invalid input changed target", e)
		}
	}
	stage := privateEnrollmentDir(t)
	os.WriteFile(filepath.Join(stage, "owner-data"), []byte("preserve"), 0600)
	e = enrollmentImageCommand(t.Context(), []string{"empty", "--data-dir", stage, "--disposable-fixture"}, nil, io.Discard, io.Discard)
	if e == nil {
		t.Fatal("nonempty target admitted")
	}
}

func privateEnrollmentDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnrollmentImageParentFailureReleasesSourceWithoutChangingIt(t *testing.T) {
	for _, failure := range []string{"disconnect", "cancel", "wrong-ack"} {
		t.Run(failure, func(t *testing.T) {
			dir := privateEnrollmentDir(t)
			store, e := core.OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			snap, e := store.UpdateSnapshot(t.Context(), update.NewProbe())
			if e != nil {
				t.Fatal(e)
			}
			store.Close()
			input, ack := io.Pipe()
			output, writer := io.Pipe()
			defer input.Close()
			defer ack.Close()
			defer output.Close()
			defer writer.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- enrollmentExportFixture(ctx, []string{"export", "--data-dir", dir, "--expected-instance", snap.InstanceID, "--disposable-fixture"}, input, writer)
			}()
			var image core.EnrollmentImage
			if e = json.NewDecoder(output).Decode(&image); e != nil {
				t.Fatal(e)
			}
			switch failure {
			case "disconnect":
				ack.Close()
			case "cancel":
				cancel()
			case "wrong-ack":
				fmt.Fprintln(ack, "RELEASE wrong")
			}
			if e = <-done; e == nil {
				t.Fatal("failed parent acknowledged cutover")
			}
			actual, lease, e := core.ExportEnrollmentImage(t.Context(), dir)
			if e != nil {
				t.Fatal(e)
			}
			defer lease.Close()
			if actual.InstanceID != image.InstanceID || core.EnrollmentImageDigest(actual) != core.EnrollmentImageDigest(image) {
				t.Fatal("failed parent changed source")
			}
		})
	}
}

// The in-process fixture owns its pipe, just as a real helper process closes
// stdout on exit. Propagate early validation failures instead of hanging Decode.
func enrollmentExportFixture(ctx context.Context, args []string, in io.Reader, out *io.PipeWriter) error {
	err := enrollmentImageCommand(ctx, args, in, out, io.Discard)
	_ = out.CloseWithError(err)
	return err
}

func TestEnrollmentImageEarlyExportFailureClosesFrame(t *testing.T) {
	dir := privateEnrollmentDir(t)
	input, ack := io.Pipe()
	output, writer := io.Pipe()
	defer input.Close()
	defer ack.Close()
	defer output.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		done <- enrollmentExportFixture(t.Context(), []string{"export", "--data-dir", dir, "--expected-instance", strings.Repeat("a", 43), "--disposable-fixture"}, input, writer)
	}()
	decoded := make(chan error, 1)
	go func() { var image core.EnrollmentImage; decoded <- json.NewDecoder(output).Decode(&image) }()
	select {
	case err := <-decoded:
		if err == nil {
			t.Fatal("invalid source emitted image")
		}
	case <-time.After(time.Second):
		t.Fatal("early fixture refusal left frame reader blocked")
	}
	if err := <-done; err == nil {
		t.Fatal("invalid source accepted")
	}
}
