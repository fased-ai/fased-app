// Maintainer-only offline validation; never installed for an end user.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/update"
)

func document(path string, out any) error {
	b, err := update.ReadDocument(path)
	if err != nil {
		return err
	}
	if update.StrictJSON(b, out) != nil {
		return errors.New("invalid public candidate input")
	}
	return nil
}
func run(args []string, out io.Writer) error {
	if os.Geteuid() == 0 {
		return errors.New("ordinary maintainer identity required")
	}
	if len(args) == 0 {
		return errors.New("verify-seed, verify-provenance, release-request or release-finalize required")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	switch args[0] {
	case "refresh-plan", "refresh-request", "refresh-finalize":
		return runRefresh(args, flags, out)
	case "release-request", "release-finalize":
		return runFinalizer(args, flags, out)
	case "verify-seed":
		path := flags.String("seed-file", "", "approved public production seed")
		pin := flags.String("seed-sha256", "", "independently approved seed digest")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *path == "" || *pin == "" {
			return errors.New("exact public seed file and SHA256 required")
		}
		b, err := update.ReadDocument(*path)
		if err != nil {
			return err
		}
		seed, err := bootstrap.ValidateProductionSeed(b, *pin)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(seed.Provenance)
	case "verify-provenance":
		policyPath := flags.String("policy-file", "", "pinned public verification material")
		expectedPath := flags.String("expected-file", "", "tag-bound archive identities")
		proofPath := flags.String("bundle-file", "", "actual GitHub attestation bundle")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *policyPath == "" || *expectedPath == "" || *proofPath == "" {
			return errors.New("policy, expected identity and actual provenance bundle required")
		}
		var policy releaseprovenance.Policy
		var expected releaseprovenance.Expected
		if err := document(*policyPath, &policy); err != nil {
			return err
		}
		if err := document(*expectedPath, &expected); err != nil {
			return err
		}
		proof, err := update.ReadDocument(*proofPath)
		if err != nil {
			return err
		}
		decision, err := releaseprovenance.Verify(policy, proof, expected, time.Now().UTC())
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(decision)
	default:
		return errors.New("no signing, publishing, installation or activation command")
	}
}
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "fased-release-candidate:", err)
		os.Exit(1)
	}
}
