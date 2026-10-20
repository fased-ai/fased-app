package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
)

func updateTrustCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("updates trust-inspect", flag.ContinueOnError)
	f.SetOutput(errOut)
	dev := f.Bool("development", false, "explicit disposable rehearsal only")
	initial := f.Bool("initial", false, "explicit first fixture observation; no missing-state fallback")
	seed := f.String("seed-file", "", "public disposable TUF seed")
	seedSHA := f.String("seed-sha256", "", "independently retained exact seed fingerprint")
	state := f.String("state-file", "", "prior disposable snapshot; never installed state")
	stateSHA := f.String("state-sha256", "", "independently retained snapshot anchor")
	now := f.Int64("fixture-time", 0, "injected Unix time; host clock is unchanged")
	ca := f.String("tls-ca-file", "", "optional controlled fixture TLS CA")
	if e := f.Parse(args); e != nil {
		return e
	}
	if !*dev || *seed == "" || *seedSHA == "" || *now <= 0 || f.NArg() != 0 || *initial && (*state != "" || *stateSHA != "") || !*initial && (*state == "" || *stateSHA == "") {
		return errors.New("explicit development seed/pin/clock and either initial mode or anchored fixture state required")
	}
	b, e := update.ReadDocument(*seed)
	if e != nil {
		return e
	}
	o := update.TrustRehearsalOptions{Development: true, Initial: *initial, Seed: b, SeedSHA256: *seedSHA, Now: *now, PreviousSHA256: *stateSHA}
	if !*initial {
		o.Previous, e = update.ReadDocument(*state)
		if e != nil {
			return e
		}
	}
	if *ca != "" {
		o.TLSCA, e = update.ReadDocument(*ca)
		if e != nil {
			return e
		}
		if len(o.TLSCA) == 0 {
			return errors.New("empty explicit fixture CA")
		}
	}
	r, e := update.RehearseTrust(context.Background(), o)
	if e != nil {
		// Failed candidate verification may still learn a valid newer root or
		// metadata floor. Expose only that read-only checkpoint, with a failing
		// exit status and all candidate/install/financial capabilities false.
		if r.Report().TrustProgressOnly {
			return errors.Join(e, json.NewEncoder(out).Encode(r.Report()))
		}
		return e
	}
	return json.NewEncoder(out).Encode(r.Report())
}
