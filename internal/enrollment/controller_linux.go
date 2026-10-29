//go:build linux

package enrollment

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

var errState = errors.New("enrollment state is pending or conflicts; preserve the original and resume the same command")

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hexID(s string, n int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == n && hex.EncodeToString(b) == s
}
func instanceID(s string) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}
func sourceValid(s sourceRecord) bool {
	if s.UID <= 0 || s.GID <= 0 || !filepath.IsAbs(s.Data) || filepath.Clean(s.Data) != s.Data || s.Data == "/" || filepath.Dir(s.Data) == "/" || strings.ContainsAny(s.Data, "\x00\r\n") || !instanceID(s.ExpectedInstanceID) || s.Profile != "local" && s.Profile != "remote" {
		return false
	}
	host, port, err := net.SplitHostPort(s.Listen)
	n, pe := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || pe != nil || n < 1024 || n > 65535 || strconv.Itoa(n) != port {
		return false
	}
	if s.PublicOrigin == "" {
		return true
	}
	u, err := url.Parse(s.PublicOrigin)
	return s.Profile == "remote" && err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery && u.String() == s.PublicOrigin && !strings.ContainsAny(s.PublicOrigin, "\r\n\t %$\\\"")
}

func (j journal) valid(owner int) bool {
	if j.Version != 1 || j.Owner != owner || owner <= 0 || !hexID(j.ID, 16) || rank(j.Phase) < 0 || !sourceValid(j.Source) || !hexID(j.CoreHash, 32) || !hexID(j.SignerHash, 32) || j.Network != "devnet" && j.Network != "mainnet-beta" || j.Node.Inode == 0 || j.Node.UID != j.Source.UID || j.Node.GID != j.Source.GID || j.Node.Mode != 0700 {
		return false
	}
	if j.Digest != "" && !hexID(j.Digest, 32) || rank(j.Phase) >= rank("importing") && !hexID(j.Digest, 32) {
		return false
	}
	if j.Target.ID != "" && (j.Target.Owner != owner || !j.Target.Enrollment || j.Target.Location != j.Source.Profile || j.Target.Listen != j.Source.Listen || j.Target.PublicOrigin != j.Source.PublicOrigin || j.Target.CoreHash != j.CoreHash || j.Target.SignerHash != j.SignerHash || j.Target.Network != j.Network || j.Target.App.UID == j.Source.UID || j.Target.Signer.UID == j.Source.UID) {
		return false
	}
	return rank(j.Phase) < rank("suspending") || j.Target.ID != ""
}

type imageLease struct {
	image   core.EnrollmentImage
	release func() error
	check   func() error
	close   func() error
}

// The private dependency seam executes the same phase controller in fixtures;
// ordinary callers cannot inject OS operations or helper transports.
type operations struct {
	owner        int
	load         func() (journal, error)
	save         func(journal) error
	node         func(string) (node, error)
	targetExists func() bool
	prepare      func(context.Context, journal, Options) (custodyinstall.Manifest, error)
	target       func(custodyinstall.Manifest) error
	emptySigner  func(context.Context, custodyinstall.Manifest) error
	export       func(context.Context, custodyinstall.Manifest, sourceRecord) (*imageLease, error)
	importImage  func(context.Context, journal, core.EnrollmentImage) error
	fence        func(context.Context, journal) error
	publish      func(context.Context, journal) error
	verifyTarget func(context.Context, journal) error
	activate     func(context.Context, journal) error
	finish       func(context.Context, journal) error
}

func run(ctx context.Context, s Source, o Options, coreHash, signerHash string, op operations) (_ custodyinstall.Manifest, resultErr error) {
	var empty custodyinstall.Manifest
	if !o.Disposable || !o.PrepareHost || s.Validate == nil || s.Suspend == nil || s.Restore == nil || !sourceValid(record(s)) || !hexID(coreHash, 32) || !hexID(signerHash, 32) || o.Network != "devnet" && o.Network != "mainnet-beta" {
		return empty, errors.New("verified Local/Remote source and explicit disposable host consent required")
	}
	j, err := op.load()
	if errors.Is(err, os.ErrNotExist) {
		if op.targetExists() {
			return empty, errors.New("an existing custody target is not enrolled by this source; preserve it")
		}
		if err = s.Validate(ctx); err != nil {
			return empty, err
		}
		n, err := op.node(s.Data)
		if err != nil || n.UID != s.UID || n.GID != s.GID || n.Mode != 0700 {
			return empty, errors.New("source directory identity changed")
		}
		var id [16]byte
		if _, err = rand.Read(id[:]); err != nil {
			return empty, err
		}
		j = journal{Version: 1, ID: hex.EncodeToString(id[:]), Owner: op.owner, Phase: "prepared", Source: record(s), Node: n, CoreHash: coreHash, SignerHash: signerHash, Network: o.Network}
		if err = op.save(j); err != nil {
			return empty, err
		}
	} else if err != nil {
		return empty, err
	}
	want, saved := record(s), j.Source
	want.Running, want.Autostart = saved.Running, saved.Autostart
	if !j.valid(op.owner) || want != saved || j.CoreHash != coreHash || j.SignerHash != signerHash || j.Network != o.Network {
		return empty, errState
	}
	set := func(phase string) error { j.Phase = phase; return op.save(j) }
	if j.Target.ID == "" {
		j.Target, err = op.prepare(ctx, j, o)
		if err != nil {
			return empty, err
		}
		if !j.valid(op.owner) || op.target(j.Target) != nil {
			return empty, errState
		}
		if err = op.save(j); err != nil {
			return empty, err
		}
	} else if err = op.target(j.Target); err != nil {
		return empty, err
	}
	if j.Phase == "complete" {
		if err = op.finish(ctx, j); err != nil {
			return j.Target, err
		}
		return j.Target, nil
	}
	// Before any possible target execution, foreign signer contents cannot be
	// adopted. Once activation was requested they must instead be preserved.
	if rank(j.Phase) < rank("activation-requested") {
		if err = op.emptySigner(ctx, j.Target); err != nil {
			return j.Target, err
		}
	}
	var lease *imageLease
	defer func() {
		if lease != nil {
			_ = lease.close()
		}
	}()
	if rank(j.Phase) < rank("fencing") {
		if err = s.Validate(ctx); err != nil {
			return j.Target, err
		}
		if n, err := op.node(j.Source.Data); err != nil || n != j.Node {
			return j.Target, errors.New("source inode changed; preserve original state")
		}
		if rank(j.Phase) < rank("suspended") {
			if err = set("suspending"); err != nil {
				return j.Target, err
			}
			if err = s.Suspend(ctx); err != nil {
				restore := s.Restore(ctx, j.Source.Running, j.Source.Autostart)
				return j.Target, errors.Join(err, restore)
			}
			if err = set("suspended"); err != nil {
				return j.Target, err
			}
		}
		lease, err = op.export(ctx, j.Target, j.Source)
		if err != nil {
			return j.Target, err
		}
		if core.ValidateEnrollmentImage(lease.image) != nil || lease.image.InstanceID != j.Source.ExpectedInstanceID {
			return j.Target, errState
		}
		digest := core.EnrollmentImageDigest(lease.image)
		if j.Digest != "" && j.Digest != digest {
			return j.Target, errors.New("source changed after an interrupted import; preserve both copies")
		}
		j.Digest = digest
		if rank(j.Phase) < rank("importing") {
			if err = set("importing"); err != nil {
				return j.Target, err
			}
		}
		if err = op.importImage(ctx, j, lease.image); err != nil {
			return j.Target, err
		}
		if lease.check == nil {
			return j.Target, errState
		}
		if err = lease.check(); err != nil {
			return j.Target, err
		}
		if err = set("staged"); err != nil {
			return j.Target, err
		}
		// From this durable point retries only move forward. Restoring a source
		// automatically could race an activation whose acknowledgement was lost.
		if err = set("fencing"); err != nil {
			return j.Target, err
		}
	}
	if rank(j.Phase) <= rank("fencing") {
		if lease == nil {
			if n, e := op.node(j.Source.Data); e == nil && n == j.Node {
				lease, err = op.export(ctx, j.Target, j.Source)
				if err != nil {
					return j.Target, err
				}
				if core.ValidateEnrollmentImage(lease.image) != nil || lease.image.InstanceID != j.Source.ExpectedInstanceID || core.EnrollmentImageDigest(lease.image) != j.Digest {
					return j.Target, errState
				}
			} else if e == nil && n.UID != 0 || e != nil && !errors.Is(e, os.ErrNotExist) {
				return j.Target, errState
			}
		}
		if lease != nil {
			if lease.check == nil {
				return j.Target, errState
			}
			if err = lease.check(); err != nil {
				return j.Target, err
			}
		}
		if err = op.fence(ctx, j); err != nil {
			return j.Target, err
		}
		if lease != nil {
			if err = lease.check(); err != nil {
				// Ambiguous source ownership never becomes permission to publish an older image.
				j.Phase = "source-lock-lost"
				return j.Target, errors.Join(err, op.save(j))
			}
		}
		if err = set("fenced"); err != nil {
			return j.Target, err
		}
	}
	if lease != nil {
		if err = lease.release(); err != nil {
			return j.Target, err
		}
		lease = nil
	}
	if rank(j.Phase) <= rank("publishing") {
		if err = set("publishing"); err != nil {
			return j.Target, err
		}
		if err = op.publish(ctx, j); err != nil {
			return j.Target, err
		}
		if err = op.verifyTarget(ctx, j); err != nil {
			return j.Target, err
		}
		if err = set("committed"); err != nil {
			return j.Target, err
		}
	}
	if rank(j.Phase) < rank("activation-requested") {
		if err = op.verifyTarget(ctx, j); err != nil {
			return j.Target, err
		}
		if err = set("activation-requested"); err != nil {
			return j.Target, err
		}
	}
	if err = op.activate(ctx, j); err != nil {
		return j.Target, err
	}
	if err = op.finish(ctx, j); err != nil {
		return j.Target, err
	}
	if err = set("complete"); err != nil {
		return j.Target, err
	}
	return j.Target, nil
}

type imageResult struct {
	Version    int    `json:"version"`
	InstanceID string `json:"instanceId"`
	Digest     string `json:"digest"`
	Empty      bool   `json:"empty,omitempty"`
}

func boundedBinary(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", errState
	}
	defer f.Close()
	i, err := f.Stat()
	if err != nil || !i.Mode().IsRegular() || i.Size() <= 0 || i.Size() > 64<<20 {
		return "", errState
	}
	b, err := io.ReadAll(io.LimitReader(f, 64<<20+1))
	if err != nil || int64(len(b)) != i.Size() {
		return "", errState
	}
	return hash(b), nil
}

func decodeSmall(raw []byte, expected, digest string, empty bool) error {
	var r imageResult
	if len(raw) > 4096 || update.StrictJSON(raw, &r) != nil || r.Version != 1 || r.InstanceID != expected || r.Digest != digest || r.Empty != empty {
		return errState
	}
	return nil
}

func imageBytes(image core.EnrollmentImage) io.Reader {
	b, _ := json.Marshal(image)
	return bytes.NewReader(b)
}

func stagePath(j journal) string { return j.Target.Root + "/enrollment-" + j.ID + ".stage" }

func releaseImage(p *custodyinstall.EnrollmentProcess, digest string) error {
	if _, err := fmt.Fprintf(p.Input, "RELEASE %s\n", digest); err != nil {
		return err
	}
	if err := p.Input.Close(); err != nil {
		return err
	}
	return p.Wait()
}
