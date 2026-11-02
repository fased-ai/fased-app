package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/update"
)

// Public inputs only. Online signing belongs to the explicit maintainer runner.
type refreshFile struct {
	Version           int                           `json:"version"`
	SeedFile          string                        `json:"seedFile"`
	SeedSHA256        string                        `json:"seedSha256"`
	PublicationFile   string                        `json:"publicationFile"`
	PublicationSHA256 string                        `json:"publicationSha256"`
	Plan              *bootstrap.ReleaseRefreshPlan `json:"plan,omitempty"`
}

func runRefresh(args []string, flags *flag.FlagSet, out io.Writer) error {
	input := flags.String("refresh-file", "", "pinned public seed, previous publication and frozen plan")
	approvals := flags.String("approvals-file", "", "online public signatures only")
	role := flags.String("role", "", "snapshot or timestamp")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *input == "" {
		return errors.New("public refresh-file required")
	}
	if args[0] == "refresh-plan" && (*approvals != "" || *role != "") || args[0] == "refresh-request" && *role == "" || args[0] == "refresh-finalize" && *role != "" {
		return errors.New("invalid refresh operation fields")
	}
	var spec refreshFile
	if err := document(*input, &spec); err != nil {
		return err
	}
	if spec.Version != 1 || spec.SeedFile == "" || spec.PublicationFile == "" {
		return errors.New("complete public refresh input required")
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(*input), p)
	}
	seed, err := update.ReadDocument(resolve(spec.SeedFile))
	if err != nil {
		return err
	}
	before, err := os.Lstat(resolve(spec.PublicationFile))
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return errors.New("regular nonsymlink publication required")
	}
	f, err := os.Open(resolve(spec.PublicationFile))
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || !os.SameFile(before, st) || st.Size() > 1<<20 {
		return errors.New("bounded regular publication required")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	v, err := bootstrap.NewReleaseRefresher(bootstrap.ReleaseRefreshInput{Seed: seed, SeedSHA256: spec.SeedSHA256, Publication: raw, PublicationSHA256: spec.PublicationSHA256, Plan: spec.Plan})
	if err != nil {
		return err
	}
	if args[0] == "refresh-plan" {
		return json.NewEncoder(out).Encode(v.Plan())
	}
	var a bootstrap.ReleaseApprovals
	if *approvals != "" {
		if err = document(*approvals, &a); err != nil {
			return err
		}
	}
	if args[0] == "refresh-request" {
		r, err := v.Request(*role, a)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	}
	r, err := v.Finalize(a)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(r)
}
