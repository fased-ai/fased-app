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
	return updateFixtureInspectCommand(args, out, errOut, false)
}

func updateFixtureInspectCommand(args []string, out, errOut io.Writer, admission bool) error {
	name := "updates trust-inspect"
	if admission {
		name = "updates release-inspect"
	}
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(errOut)
	dev := f.Bool("development", false, "explicit disposable rehearsal only")
	initial := f.Bool("initial", false, "explicit first fixture observation; no missing-state fallback")
	seed := f.String("seed-file", "", "public disposable TUF seed")
	seedSHA := f.String("seed-sha256", "", "independently retained exact seed fingerprint")
	state := f.String("state-file", "", "prior disposable snapshot; never installed state")
	stateSHA := f.String("state-sha256", "", "independently retained snapshot anchor")
	now := f.Int64("fixture-time", 0, "injected Unix time; host clock is unchanged")
	ca := f.String("tls-ca-file", "", "optional controlled fixture TLS CA")
	var policy, policySHA, evidence, artifacts string
	if admission {
		f.StringVar(&policy, "policy-file", "", "public disposable builder admission policy")
		f.StringVar(&policySHA, "policy-sha256", "", "independently retained exact policy fingerprint")
		f.StringVar(&evidence, "evidence-file", "", "TUF-bound fixture release evidence")
		f.StringVar(&artifacts, "artifact-dir", "", "absolute four-file inert fixture directory; no execution")
	}
	if e := f.Parse(args); e != nil {
		return e
	}
	if !*dev || *seed == "" || *seedSHA == "" || *now <= 0 || f.NArg() != 0 || *initial && (*state != "" || *stateSHA != "") || !*initial && (*state == "" || *stateSHA == "") {
		return errors.New("explicit development seed/pin/clock and either initial mode or anchored fixture state required")
	}
	if admission && (policy == "" || policySHA == "" || evidence == "" || artifacts == "") {
		return errors.New("explicit pinned policy, evidence and inert artifact directory required")
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
	if admission {
		p, e := update.ReadDocument(policy)
		if e != nil {
			return e
		}
		b, e := update.ReadDocument(evidence)
		if e != nil {
			return e
		}
		_, r, e := update.AdmitFixtureRelease(context.Background(), o, update.ReleaseAdmissionOptions{Policy: p, PolicySHA256: policySHA, Evidence: b, ArtifactDir: artifacts})
		if e != nil {
			if r.TrustProgressOnly {
				return errors.Join(e, json.NewEncoder(out).Encode(r))
			}
			return e
		}
		return json.NewEncoder(out).Encode(r)
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
