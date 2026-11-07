package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/fased-ai/fased-app/internal/enrollment"
)

func nativeEnrollmentResume(args []string, out io.Writer) error {
	f := flag.NewFlagSet("enrollment-resume", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	instance := f.String("expected-instance", "", "")
	profile := f.String("source", "", "")
	network := f.String("network", "", "")
	coreHash := f.String("core-sha256", "", "")
	signerHash := f.String("signer-sha256", "", "")
	native := f.Bool("native-owner", false, "")
	if f.Parse(args) != nil || f.NArg() != 0 {
		return errors.New("invalid enrollment reconciliation")
	}
	ok, err := enrollment.CanResumeNative(*instance, *profile, *network, *coreHash, *signerHash, *native)
	recover := false
	if err == nil && !ok {
		recover, err = enrollment.CanRecoverNative(*instance, *profile, *network, *coreHash, *signerHash, *native)
		ok = recover && err == nil
	}
	if err != nil || !ok {
		return errors.New("enrollment does not match the retained owner operation")
	}
	return json.NewEncoder(out).Encode(struct {
		Version          int  `json:"version"`
		Resume           bool `json:"resume"`
		RecoverSuspended bool `json:"recoverSuspended,omitempty"`
	}{1, true, recover})
}
