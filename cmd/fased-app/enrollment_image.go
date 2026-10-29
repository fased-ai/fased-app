package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
)

// This private owner-helper channel carries app metadata/ciphertext, not signer
// material. It is never exposed as a core HTTP/IPC action or normal UI editor.
func enrollmentImageCommand(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return errors.New("ordinary enrollment helper required")
	}
	action := args[0]
	if action != "export" && action != "import" && action != "verify" && action != "empty" {
		return errors.New("unsupported enrollment image action")
	}
	f := flag.NewFlagSet("enrollment-image", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("data-dir", "", "private application state")
	expected := f.String("expected-instance", "", "exact source instance")
	digest := f.String("digest", "", "exact image content identity")
	disposable := f.Bool("disposable-fixture", false, "disposable engineering enrollment only")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 || !*disposable || !filepath.IsAbs(*dir) || filepath.Clean(*dir) != *dir {
		return errors.New("absolute private directory and disposable enrollment consent required")
	}
	if action == "empty" {
		if *expected != "" || *digest != "" {
			return errors.New("empty inspection accepts no image identity")
		}
		info, e := os.Lstat(*dir)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("private stage directory required")
		}
		if e = core.EnsurePrivateDirectory(*dir); e != nil {
			return e
		}
		files, e := os.ReadDir(*dir)
		if e != nil {
			return e
		}
		if len(files) != 0 {
			return errors.New("target directory is not empty; preserving it")
		}
		return json.NewEncoder(out).Encode(struct {
			Version int  `json:"version"`
			Empty   bool `json:"empty"`
		}{1, true})
	}
	if *expected == "" || len(*expected) > 128 || strings.ContainsAny(*expected, " \t\r\n\x00") {
		return errors.New("exact expected instance required")
	}
	if action == "export" && *digest != "" {
		return errors.New("export content identity is derived, never supplied")
	}
	if action != "export" {
		b, e := hex.DecodeString(*digest)
		if e != nil || len(b) != 32 || hex.EncodeToString(b) != *digest {
			return errors.New("exact image digest required")
		}
	}
	var image core.EnrollmentImage
	if (action == "import" || action == "export") && in == nil {
		return errors.New("private enrollment input required")
	}
	if action == "import" {
		// The parent owns a finite private pipe/file. Never accept a second frame or
		// unbounded input, and validate everything before creating a stage file.
		raw, e := io.ReadAll(io.LimitReader(in, core.MaxEnrollmentEncodedBytes+1))
		if e != nil {
			return errors.New("invalid private enrollment image")
		}
		image, e = core.DecodeEnrollmentImage(raw)
		if e != nil {
			return errors.New("invalid private enrollment image")
		}

		if image.InstanceID != *expected || core.EnrollmentImageDigest(image) != *digest {
			return errors.New("enrollment image identity changed")
		}
		if e := core.ValidateEnrollmentImage(image); e != nil {
			return e
		}
		if e := core.ImportEnrollmentImage(ctx, *dir, image); e != nil {
			return fmt.Errorf("enrollment import stage: %w", e)
		}
		return enrollmentImageResult(out, image)
	}
	image, lock, e := core.ExportEnrollmentImage(ctx, *dir)
	if e != nil {
		return e
	}
	defer lock.Close()
	if image.InstanceID != *expected {
		return errors.New("source instance differs; preserving its state")
	}
	if action == "verify" {
		if core.EnrollmentImageDigest(image) != *digest {
			return errors.New("staged enrollment image changed")
		}
		return enrollmentImageResult(out, image)
	}
	if e = json.NewEncoder(out).Encode(image); e != nil {
		return e
	}
	// Hold the source lock across the owner's atomic filesystem fence. A lost
	// parent closes stdin; it never acknowledges a successful cutover.
	answer := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(io.LimitReader(in, 1024))
		for {
			line, e := reader.ReadString('\n')
			if len(line) > 128 {
				answer <- errors.New("enrollment acknowledgement too long")
				return
			}
			if e != nil {
				answer <- e
				return
			}
			switch line {
			case "CHECK " + core.EnrollmentImageDigest(image) + "\n":
				_, e = io.WriteString(out, "LOCKED "+core.EnrollmentImageDigest(image)+"\n")
				if e != nil {
					answer <- e
					return
				}
			case "RELEASE " + core.EnrollmentImageDigest(image) + "\n":
				answer <- nil
				return
			default:
				answer <- errors.New("source fence acknowledgement changed")
				return
			}
		}
	}()
	select {
	case e := <-answer:
		return e
	case <-ctx.Done():
		if c, ok := in.(io.Closer); ok {
			_ = c.Close()
		}
		return ctx.Err()
	}
}

func enrollmentImageResult(out io.Writer, image core.EnrollmentImage) error {
	return json.NewEncoder(out).Encode(struct {
		Version    int    `json:"version"`
		InstanceID string `json:"instanceId"`
		Digest     string `json:"digest"`
	}{1, image.InstanceID, core.EnrollmentImageDigest(image)})
}
