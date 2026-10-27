package update

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AcquisitionOptions has no public defaults. TrustSHA256 must be obtained
// independently of the download origin. Inventory remains disposable input;
// this verifier neither reads nor advances the installed/reserved sequence.
type AcquisitionOptions struct {
	Development bool
	Origin      string
	ManifestURL string
	Trust       []byte
	TrustSHA256 string
	Inventory   []byte
	Directory   string
	TLSCA       []byte // Optional explicit development CA; TLS verification stays enabled.
}

type AcquisitionReceipt struct {
	Version         int    `json:"version"`
	DevelopmentOnly bool   `json:"developmentOnly"`
	Origin          string `json:"origin"`
	ManifestURL     string `json:"manifestUrl"`
	TrustSHA256     string `json:"trustSha256"`
	EnvelopeSHA256  string `json:"envelopeSha256"`
	Plan            Plan   `json:"plan"`
}

// DevelopmentAcquisitionURL shares the canonical, credential-free HTTPS policy
// with the separately gated initial installer. It grants no release authority.
func DevelopmentAcquisitionURL(raw string) (*url.URL, error) { return acquisitionURL(raw) }

// DevelopmentAcquisitionClient retains anonymous/no-proxy/no-redirect transport;
// an explicit test CA is usable only by callers' development admission paths.
func DevelopmentAcquisitionClient(ca []byte) (*http.Client, *http.Transport, error) {
	return acquisitionClient(ca)
}

func DevelopmentAcquisitionResponse(ctx context.Context, client *http.Client, address string, limit int64) (*http.Response, error) {
	return acquisitionResponse(ctx, client, address, limit)
}

func acquisitionURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || u.String() != raw {
		return nil, errors.New("explicit canonical HTTPS URL without credentials, escapes, query or fragment required")
	}
	if u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || (u.Path != "" && path.Clean(u.Path) != u.Path) || strings.ContainsAny(raw, "%\\ \t\r\n") {
		return nil, errors.New("invalid HTTPS origin/path")
	}
	return u, nil
}

func acquisitionClient(ca []byte) (*http.Client, *http.Transport, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(ca) != 0 {
		if len(ca) > MaxDocument {
			return nil, nil, errors.New("development CA too large")
		}
		config.RootCAs = x509.NewCertPool()
		if !config.RootCAs.AppendCertsFromPEM(ca) {
			return nil, nil, errors.New("invalid development TLS CA")
		}
	}
	transport := &http.Transport{
		// No environment proxy, cookies, credentials, automatic decompression or
		// redirect authority is inherited from another client or the process.
		TLSClientConfig: config, DisableCompression: true,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("development acquisition forbids every redirect")
	}}
	return client, transport, nil
}

func acquisitionResponse(ctx context.Context, client *http.Client, address string, limit int64) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(r)
	if err != nil {
		return nil, errors.New("HTTPS acquisition failed (TLS, redirect, timeout or transport)")
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "" || response.Header.Get("Content-Range") != "" || response.ContentLength > limit {
		_ = response.Body.Close()
		return nil, errors.New("complete bounded unencoded HTTP 200 response required")
	}
	return response, nil
}

func writeAcquisitionFile(root *os.Root, name string, b []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// AcquireDevelopment authenticates a pinned development trust document, fetches
// one signed manifest and its complete pair from one explicit HTTPS origin,
// then rechecks validity and bytes. It never installs/executes artifacts, opens
// instance state, calls an owner/signer API, or provides a background updater.
// Output is exclusive/private and never reused on retry. A success receipt is
// written last; power-loss leftovers must be reverified, never treated as authority.
func AcquireDevelopment(ctx context.Context, o AcquisitionOptions) (result AcquisitionReceipt, err error) {
	if !o.Development || !hashOK(o.TrustSHA256) || digest(o.Trust) != o.TrustSHA256 {
		return result, errors.New("explicit development mode and independently pinned trust SHA-256 required")
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return result, errors.New("private acquisition requires an ordinary Linux/macOS user; Windows ACL acceptance remains unavailable")
	}
	origin, err := acquisitionURL(o.Origin)
	if err != nil || origin.Path != "" {
		return result, errors.New("explicit HTTPS origin without a path required")
	}
	manifestURL, err := acquisitionURL(o.ManifestURL)
	if err != nil || manifestURL.Host != origin.Host || manifestURL.Path == "" || strings.HasSuffix(manifestURL.Path, "/") {
		return result, errors.New("manifest must be a file on the exact pinned HTTPS origin")
	}
	trust, err := ParseTrust(o.Trust, time.Now().Unix())
	if err != nil {
		return result, err
	}
	var inv Inventory
	if err = StrictJSON(o.Inventory, &inv); err != nil {
		return result, err
	}
	if !filepath.IsAbs(o.Directory) || filepath.Clean(o.Directory) != o.Directory {
		return result, errors.New("absolute clean exclusive output directory required")
	}
	parent, base := filepath.Dir(o.Directory), filepath.Base(o.Directory)
	before, err := os.Lstat(parent)
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0700 || !acquisitionParentOwned(before) {
		return result, errors.New("output parent must be a real private 0700 directory owned by this user")
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return result, err
	}
	defer parentRoot.Close()
	after, err := parentRoot.Stat(".")
	if err != nil || !os.SameFile(before, after) || after.Mode().Perm() != 0700 || !acquisitionParentOwned(after) {
		return result, errors.New("output parent changed")
	}
	if _, e := parentRoot.Lstat(base); !os.IsNotExist(e) {
		return result, errors.New("output must not already exist; no overwrite or resume")
	}
	client, transport, err := acquisitionClient(o.TLSCA)
	if err != nil {
		return result, err
	}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	response, err := acquisitionResponse(ctx, client, o.ManifestURL, MaxDocument)
	if err != nil {
		return result, err
	}
	mb, err := io.ReadAll(io.LimitReader(response.Body, MaxDocument+1))
	err = errors.Join(err, response.Body.Close())
	if err != nil || len(mb) > MaxDocument {
		return result, errors.New("manifest incomplete or oversized")
	}
	m, payloadSHA, err := Authenticate(mb, trust, time.Now().Unix())
	if err != nil {
		return result, err
	}
	if _, err = Compatible(m, payloadSHA, inv); err != nil {
		return result, err
	}
	reserved := map[string]bool{"manifest.json": true, "trust.json": true, "inventory.json": true, "acquisition.json": true}
	for _, c := range m.Components {
		if reserved[strings.ToLower(c.Artifact.File)] {
			return result, errors.New("artifact conflicts with acquisition metadata")
		}
	}
	if err = parentRoot.Mkdir(base, 0700); err != nil {
		return result, err
	}
	created, err := parentRoot.Lstat(base)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			current, e := parentRoot.Lstat(base)
			if e == nil && os.SameFile(created, current) {
				err = errors.Join(err, parentRoot.RemoveAll(base))
			} else {
				err = errors.Join(err, errors.New("output identity changed; preserve it for inspection"))
			}
			result = AcquisitionReceipt{}
		}
	}()
	root, err := parentRoot.OpenRoot(base)
	if err != nil {
		return result, err
	}
	defer root.Close()
	for _, c := range m.Components {
		a := c.Artifact
		address := *manifestURL
		address.Path = path.Join(path.Dir(manifestURL.Path), a.File)
		response, err = acquisitionResponse(ctx, client, address.String(), a.Size)
		if err != nil {
			return result, fmt.Errorf("%s artifact: %w", c.Contract.Component, err)
		}
		f, e := root.OpenFile(a.File, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			_ = response.Body.Close()
			return result, e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(response.Body, a.Size+1))
		e = errors.Join(e, response.Body.Close(), f.Sync(), f.Close())
		if e != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
			return result, errors.New("artifact incomplete, size/hash mismatch or output write failed")
		}
	}
	// Reauthenticate expiry after bounded downloads and rehash all local bytes.
	p, err := Inspect(mb, o.Trust, o.Inventory, o.Directory, time.Now().Unix())
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	for _, doc := range []struct {
		name  string
		bytes []byte
	}{{"manifest.json", mb}, {"trust.json", o.Trust}, {"inventory.json", o.Inventory}} {
		if err = writeAcquisitionFile(root, doc.name, doc.bytes); err != nil {
			return result, err
		}
	}
	result = AcquisitionReceipt{Version: 1, DevelopmentOnly: true, Origin: o.Origin, ManifestURL: o.ManifestURL, TrustSHA256: o.TrustSHA256, EnvelopeSHA256: digest(mb), Plan: p}
	b, err := json.Marshal(result)
	if err != nil {
		return AcquisitionReceipt{}, err
	}
	err = writeAcquisitionFile(root, "acquisition.json", b)
	return result, err
}
