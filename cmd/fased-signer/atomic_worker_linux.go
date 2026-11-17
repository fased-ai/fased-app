//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func validateReusableSetupCommand(args []string, input io.Reader, out, errOut io.Writer) error {
	f := flag.NewFlagSet("fixture-validate-reusable-setup", flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", 0, "managed installation owner")
	disposable := f.Bool("disposable-fixture", false, "unfunded fixture custody")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !*disposable || *owner <= 0 {
		return errors.New("exact disposable maintenance setup required")
	}
	if err := custodyinstall.VerifyReusableSetup(context.Background(), *owner, input); err != nil {
		return err
	}
	_, err := fmt.Fprint(out, "FASED_SETUP_READY=yes")
	return err
}

func validateStageRecoveryCommand(args []string, errOut io.Writer) error {
	if len(args) == 1 && args[0] == "--worker-capability" {
		return nil
	}
	f := flag.NewFlagSet("fixture-validate-stage-recovery", flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", 0, "managed installation owner")
	kind := f.String("kind", "", "fixed staging class")
	slot := f.String("slot", "", "fixed staging slot")
	copy := f.Bool("copy", false, "validate the frozen staged copy")
	worker := f.Bool("worker", false, "validate the selected staging worker")
	disposable := f.Bool("disposable-fixture", false, "unfunded fixture custody")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !*disposable || *owner <= 0 || (*copy && *worker) {
		return errors.New("exact disposable staging recovery required")
	}
	if *copy {
		return custodyinstall.VerifyReusableStageCopy(*owner, *kind, *slot)
	}
	if *worker {
		return custodyinstall.VerifyReusableStageWorker(*owner, *kind, *slot)
	}
	return custodyinstall.VerifyReusableStageRecovery(*owner, *kind, *slot)
}

func validatePreparationStartCommand(args []string, errOut io.Writer) error {
	f := flag.NewFlagSet("fixture-validate-preparation-start", flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", 0, "managed installation owner")
	disposable := f.Bool("disposable-fixture", false, "unfunded fixture custody")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !*disposable || *owner <= 0 {
		return errors.New("exact disposable preparation start required")
	}
	return custodyinstall.VerifyReusablePreparationStart(*owner)
}

func validatePreparationFenceCommand(args []string, errOut io.Writer) error {
	if len(args) == 1 && args[0] == "--capability" {
		return nil
	}
	f := flag.NewFlagSet("fixture-validate-preparation-fence", flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", 0, "managed installation owner")
	disposable := f.Bool("disposable-fixture", false, "unfunded fixture custody")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !*disposable || *owner <= 0 {
		return errors.New("exact disposable preparation fence required")
	}
	return custodyinstall.VerifyReusablePreparationFence(*owner)
}

func preparationHoldCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("fixture-preparation-hold", flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", 0, "managed installation owner")
	intent := f.String("intent-id", "", "exact original intent")
	disposable := f.Bool("disposable-fixture", false, "unfunded fixture custody")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !*disposable || *owner <= 0 {
		return errors.New("exact disposable preparation hold required")
	}
	i, err := custodyinstall.ReadReusablePreparationHold(*owner, *intent)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(i)
}

// Only a pinned signer running as the configured signer UID can validate a
// transaction worker. This entry accepts no credentials or arbitrary paths.
func validateAtomicWorkerCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("fixture-validate-atomic-worker", flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", 0, "managed installation owner")
	disposable := f.Bool("disposable-fixture", false, "unfunded fixture custody")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !*disposable || *owner <= 0 {
		return errors.New("exact disposable atomic worker required")
	}
	name, requestID, err := custodyinstall.VerifyReusableAtomicRequest(context.Background(), *owner)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "FASED_REQUEST_ID=%s\nFASED_WORKER_UNIT=%s\n", requestID, name)
	return err
}

func validateAtomicProvisionCommand(args []string, out, errOut io.Writer) error {
	if len(args) == 1 && args[0] == "--verify-only-capability" {
		return nil
	}
	if len(args) == 1 && args[0] == "--published-capability" {
		return nil
	}
	f := flag.NewFlagSet("fixture-validate-atomic-provision", flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", 0, "managed installation owner")
	disposable := f.Bool("disposable-fixture", false, "unfunded fixture custody")
	verifyOnly := f.Bool("verify-only", false, "recheck without writing the protected environment")
	verifyPublished := f.Bool("verify-published", false, "recheck the published and loaded transaction workers")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !*disposable || *owner <= 0 || (*verifyOnly && *verifyPublished) {
		return errors.New("exact disposable preparation required")
	}
	if *verifyPublished {
		_, err := custodyinstall.VerifyReusableAtomicPublished(context.Background(), *owner)
		return err
	}
	id, err := custodyinstall.VerifyReusableAtomicProvisionWorker(*owner)
	if err != nil {
		return err
	}
	if *verifyOnly {
		return nil
	}
	_, err = fmt.Fprintf(out, "FASED_TXID=%s", id)
	return err
}
