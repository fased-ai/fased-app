package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/fased-ai/fased-app/internal/update"
)

func updateAcquireCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("updates acquire", flag.ContinueOnError)
	f.SetOutput(errOut)
	dev := f.Bool("development", false, "explicit disposable development acquisition; no installation authority")
	origin := f.String("origin", "", "exact HTTPS origin, without path")
	url := f.String("manifest-url", "", "signed development manifest on that origin; redirects forbidden")
	trust := f.String("trust-file", "", "local disposable public trust document")
	pin := f.String("trust-sha256", "", "independently obtained SHA-256 of exact trust document bytes")
	inventory := f.String("inventory", "", "local disposable inventory, never installed-state authority")
	directory := f.String("output", "", "absent absolute output directory under existing private 0700 parent")
	ca := f.String("tls-ca-file", "", "optional controlled fixture CA; never disables TLS verification")
	if err := f.Parse(args); err != nil {
		return err
	}
	if !*dev || f.NArg() != 0 || *origin == "" || *url == "" || *trust == "" || *pin == "" || *inventory == "" || *directory == "" {
		return errors.New("explicit development mode, HTTPS origin/manifest, pinned trust, fixture inventory and exclusive output required")
	}
	tb, err := update.ReadDocument(*trust)
	if err != nil {
		return err
	}
	ib, err := update.ReadDocument(*inventory)
	if err != nil {
		return err
	}
	var cb []byte
	if *ca != "" {
		cb, err = update.ReadDocument(*ca)
		if err != nil {
			return err
		}
		if len(cb) == 0 {
			return errors.New("empty explicit TLS CA")
		}
	}
	r, err := update.AcquireDevelopment(context.Background(), update.AcquisitionOptions{Development: *dev, Origin: *origin, ManifestURL: *url, Trust: tb, TrustSHA256: *pin, Inventory: ib, Directory: *directory, TLSCA: cb})
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(r)
}
