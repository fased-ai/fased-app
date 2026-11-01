package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

// Every input is explicit public data. There is no key, signing, clock, TLS or
// endpoint override and no source/environment default for production trust.
type releaseCandidateFile struct {
	Version        int                   `json:"version"`
	SeedFile       string                `json:"seedFile"`
	SeedSHA256     string                `json:"seedSha256"`
	ArchiveFile    string                `json:"archiveFile"`
	ProvenanceFile string                `json:"provenanceFile"`
	Plan           bootstrap.ReleasePlan `json:"plan"`
}

func candidateFinalizer(candidate, work string) (*bootstrap.ReleaseFinalizer, error) {
	var input releaseCandidateFile
	if err := document(candidate, &input); err != nil {
		return nil, err
	}
	if input.Version != 1 || input.SeedFile == "" || input.ArchiveFile == "" || input.ProvenanceFile == "" {
		return nil, errors.New("complete public candidate file required")
	}
	base, err := filepath.Abs(filepath.Dir(candidate))
	if err != nil {
		return nil, err
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	seed, err := update.ReadDocument(resolve(input.SeedFile))
	if err != nil {
		return nil, err
	}
	proof, err := update.ReadDocument(resolve(input.ProvenanceFile))
	if err != nil {
		return nil, err
	}
	return bootstrap.NewReleaseFinalizer(bootstrap.ReleaseFinalizerInput{Seed: seed, SeedSHA256: input.SeedSHA256, ArchiveFile: resolve(input.ArchiveFile), Provenance: proof, WorkDirectory: work, Plan: input.Plan})
}
func runFinalizer(args []string, flags *flag.FlagSet, out io.Writer) error {
	candidate := flags.String("candidate-file", "", "explicit public candidate and release plan")
	approvals := flags.String("approvals-file", "", "detached public signatures; private keys prohibited")
	work := flags.String("work-dir", "", "existing private publisher scratch directory")
	role := flags.String("role", "", "targets, snapshot or timestamp")
	output := flags.String("output", "", "exclusive new metadata directory")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *candidate == "" || !filepath.IsAbs(*work) || filepath.Clean(*work) != *work {
		return errors.New("candidate-file and absolute private work-dir required")
	}
	request := args[0] == "release-request"
	if request && (*output != "" || *role == "") || !request && (*role != "" || *approvals == "" || !filepath.IsAbs(*output) || filepath.Clean(*output) != *output) {
		return errors.New("request needs role; finalize needs approvals and exclusive absolute output")
	}
	f, err := candidateFinalizer(*candidate, *work)
	if err != nil {
		return err
	}
	var a bootstrap.ReleaseApprovals
	if *approvals != "" {
		if err = document(*approvals, &a); err != nil {
			return err
		}
	}
	if request {
		r, err := f.Request(*role, a)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	}
	finalized, err := f.Finalize(a)
	if err != nil {
		return err
	}
	if err = writeFinalized(*output, finalized); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(finalized.Report)
}
func writeFinalized(output string, release bootstrap.FinalizedRelease) error {
	// A pre-existing output, including failed output, is never overwritten/adopted.
	if err := core.EnsurePrivateDirectory(filepath.Dir(output)); err != nil {
		return err
	}
	if err := os.Mkdir(output, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(output)
	if err != nil {
		return err
	}
	defer root.Close()
	names := make([]string, 0, len(release.Files))
	for name := range release.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	write := func(name string, b []byte) error {
		if !filepath.IsLocal(name) || filepath.Clean(name) != name {
			return errors.New("fixed relative metadata output required")
		}
		if err := root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = file.Write(b)
		return errors.Join(err, file.Sync(), file.Close())
	}
	for _, name := range names {
		if err = write(name, release.Files[name]); err != nil {
			return err
		}
	}
	receipt, err := json.Marshal(release.Report)
	if err != nil {
		return err
	}
	if err = write("finalization.json", append(receipt, '\n')); err != nil {
		return err
	}
	publication, err := json.Marshal(release.Publication)
	if err != nil {
		return err
	}
	if err = write("publication.json", append(publication, '\n')); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return fmt.Errorf("metadata output sync failed: %w", err)
	}
	return nil
}
