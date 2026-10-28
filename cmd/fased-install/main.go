// Independent initial verifier. No public seed, endpoint, key or activation is
// compiled in; first-install trust must be supplied independently.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/update"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fased-install:", err)
		os.Exit(1)
	}
}
func run(args []string, in io.Reader, out, errOut io.Writer) error {
	f := flag.NewFlagSet("fased-install", flag.ContinueOnError)
	f.SetOutput(errOut)
	development := f.Bool("development", false, "explicit disposable-trust development bootstrap")
	seed := f.String("seed-file", "", "independently obtained first-install seed")
	pin := f.String("seed-sha256", "", "independently authenticated seed SHA-256")
	directory := f.String("output", "", "absent absolute output under private0700 parent")
	ca := f.String("tls-ca-file", "", "optional controlled HTTPS fixture CA")
	install := f.Bool("install", false, "after verification, explicitly open the existing installation wizard")
	prefix := f.String("prefix", "", "optional explicit installation directory")
	data := f.String("data-dir", "", "optional explicit instance data directory")
	start := f.Bool("start", true, "start the user service after owner-reviewed installation")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *seed == "" || *pin == "" || *directory == "" || f.NArg() != 0 {
		return errors.New("independent seed, authenticated pin and exclusive output are required; public activation remains disabled")
	}
	if !*install && (*prefix != "" || *data != "" || !*start) {
		return errors.New("installation options require explicit --install")
	}
	b, err := update.ReadDocument(*seed)
	if err != nil {
		return err
	}
	var cert []byte
	if *ca != "" {
		cert, err = update.ReadDocument(*ca)
		if err != nil {
			return err
		}
		if len(cert) == 0 {
			return errors.New("empty fixture CA")
		}
	}
	p, err := bootstrap.Prepare(context.Background(), bootstrap.Options{Development: *development, Seed: b, SeedSHA256: *pin, Directory: *directory, TLSCA: cert})
	if err != nil {
		return err
	}
	defer p.Close()
	if *install {
		if err = p.Run(context.Background(), bootstrap.InstallOptions{Prefix: *prefix, DataDirectory: *data, Start: *start}, in, out, errOut); err != nil {
			return err
		}
	}
	return json.NewEncoder(out).Encode(p.Report())
}
