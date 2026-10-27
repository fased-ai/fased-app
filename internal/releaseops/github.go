package releaseops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/fased-ai/fased-app/internal/update"
)

const GitHubAPIVersion = "2026-03-10"
const githubMaxMetadata = 64 << 10
const githubMaxArtifact = 256 << 20
const githubMaxMetadataFiles = 16
const githubMaxRootSteps = 32
const githubMaxAssets = 16

// Policy is seed-locked configuration, never learned from an API response.
// It authenticates no release itself; callers must authorize locators with TUF.
type GitHubPolicy struct {
	Version        int    `json:"version"`
	Repository     string `json:"repository"`
	RepositoryID   string `json:"repositoryId"`
	MetadataBranch string `json:"metadataBranch"`
	MetadataHost   string `json:"metadataHost"`
	APIHost        string `json:"apiHost"`
	CDNHost        string `json:"cdnHost"`
	CDNPathPrefix  string `json:"cdnPathPrefix"`
	APIVersion     string `json:"apiVersion"`
}

type GitHubAsset struct {
	RepositoryID string `json:"repositoryId"`
	ReleaseID    string `json:"releaseId"`
	AssetID      string `json:"assetId"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	CDNPath      string `json:"cdnPath"`
}

func canonicalPath(s string) bool {
	return len(s) > 0 && len(s) <= 2048 && strings.HasPrefix(s, "/") && path.Clean(s) == s && !strings.ContainsAny(s, "%?#\\ \t\r\n\x00")
}

func (p GitHubPolicy) Validate() error {
	if p.Version != 1 || p.Repository != "fased-ai/fased-app" || !number(p.RepositoryID) || p.MetadataHost != "raw.githubusercontent.com" || p.APIHost != "api.github.com" || p.CDNHost != "release-assets.githubusercontent.com" || p.APIVersion != GitHubAPIVersion || p.CDNPathPrefix != "/github-production-release-asset/"+p.RepositoryID+"/" {
		return errors.New("explicit pinned GitHub repository/hosts/path/API policy required")
	}
	if p.MetadataBranch == "" || len(p.MetadataBranch) > 80 || p.MetadataBranch == "." || p.MetadataBranch == ".." {
		return errors.New("invalid protected metadata branch")
	}
	for _, c := range p.MetadataBranch {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c)) {
			return errors.New("invalid protected metadata branch")
		}
	}
	return nil
}

func (a GitHubAsset) Validate(p GitHubPolicy) error {
	if p.Validate() != nil || a.RepositoryID != p.RepositoryID || !number(a.ReleaseID) || !number(a.AssetID) || !safeName(a.Name) || a.Size <= 0 || a.Size > githubMaxArtifact || !hashOK(a.SHA256) || !canonicalPath(a.CDNPath) || !strings.HasPrefix(a.CDNPath, p.CDNPathPrefix) {
		return errors.New("signed GitHub repository/release/asset/target binding required")
	}
	return nil
}

type GitHubTransport struct {
	mu                      sync.Mutex
	policy                  GitHubPolicy
	wire                    *transport
	deadline                time.Time
	metadata, roots, assets int
	closed                  bool
	repositoryChecked       bool
	releases                map[string]map[string]githubAssetIdentity
}
type githubAssetIdentity struct {
	Name  string
	Size  int64
	State string
}

// NewGitHubTransport has no test-CA, endpoint, resolver or dial override. An
// application must independently approve a seed before constructing it.
func NewGitHubTransport(p GitHubPolicy) (*GitHubTransport, error) {
	return newGitHubTransport(p, nil, nil, nil)
}

// NewControlledGitHubTransport confines explicit test wiring to a separate API;
// normal installer configuration never reaches this constructor.
func NewControlledGitHubTransport(p GitHubPolicy, ca []byte, lookup func(context.Context, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) (*GitHubTransport, error) {
	if len(ca) == 0 || lookup == nil || dial == nil {
		return nil, errors.New("controlled GitHub fixtures require explicit CA, DNS and dial hooks")
	}
	return newGitHubTransport(p, ca, lookup, dial)
}

func newGitHubTransport(p GitHubPolicy, ca []byte, lookup resolver, dial dialer) (*GitHubTransport, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	w, err := newHostTransport(ca, []string{p.MetadataHost, p.APIHost, p.CDNHost}, lookup, dial)
	if err != nil {
		return nil, err
	}
	return &GitHubTransport{policy: p, wire: w, deadline: time.Now().Add(2 * time.Minute), releases: map[string]map[string]githubAssetIdentity{}}, nil
}
func (g *GitHubTransport) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	g.wire.wire.CloseIdleConnections()
	return nil
}

func (g *GitHubTransport) context(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if g.closed || !time.Now().Before(g.deadline) {
		return nil, nil, errors.New("GitHub refresh closed or expired")
	}
	c, cancel := context.WithDeadline(ctx, g.deadline)
	if err := c.Err(); err != nil {
		cancel()
		return nil, nil, errors.New("GitHub refresh cancelled")
	}
	return c, cancel, nil
}

func metadataPath(name string) (root bool, ok bool) {
	if name == "timestamp.json" {
		return false, true
	}
	parts := strings.Split(name, ".")
	if len(parts) == 3 && parts[2] == "json" && number(parts[0]) && (parts[1] == "root" || parts[1] == "snapshot" || parts[1] == "targets") {
		n, err := strconv.ParseUint(parts[0], 10, 31)
		return parts[1] == "root", err == nil && n > 0
	}
	const pre = "targets/bootstrap/"
	const suffix = ".descriptor.json"
	if strings.HasPrefix(name, pre) && strings.HasSuffix(name, suffix) {
		return false, hashOK(strings.TrimSuffix(strings.TrimPrefix(name, pre), suffix))
	}
	return false, false
}

func (g *GitHubTransport) Metadata(ctx context.Context, name string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, ok := metadataPath(name)
	if !ok {
		return nil, errors.New("GitHub metadata path refused")
	}
	if root {
		g.roots++
		if g.roots > githubMaxRootSteps {
			return nil, errors.New("GitHub root-step budget exhausted")
		}
	} else {
		g.metadata++
		if g.metadata > githubMaxMetadataFiles {
			return nil, errors.New("GitHub metadata budget exhausted")
		}
	}
	ctx, cancel, err := g.context(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	u := "https://" + g.policy.MetadataHost + "/" + g.policy.Repository + "/" + g.policy.MetadataBranch + "/" + name
	r, err := g.request(ctx, u, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	return githubRead(r, githubMaxMetadata, 0, false)
}

func (g *GitHubTransport) request(ctx context.Context, address, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("GitHub request refused")
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "Fased-Installer")
	if req.URL.Host == g.policy.APIHost {
		req.Header.Set("X-GitHub-Api-Version", g.policy.APIVersion)
	}
	r, err := g.wire.client.Do(req)
	if err != nil {
		return nil, errors.New("GitHub HTTPS response unavailable")
	}
	return r, nil
}

func githubRead(r *http.Response, limit, exact int64, jsonBody bool) ([]byte, error) {
	if r == nil || r.Body == nil {
		return nil, errors.New("GitHub response unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK || r.Header.Get("Content-Encoding") != "" || r.Header.Get("Content-Range") != "" || r.ContentLength > limit || exact > 0 && r.ContentLength != exact {
		return nil, errors.New("GitHub complete bounded unencoded200 response required")
	}
	if jsonBody {
		kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || (kind != "application/json" && kind != "application/vnd.github+json") {
			return nil, errors.New("GitHub identity JSON required")
		}
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(b)) > limit || exact > 0 && int64(len(b)) != exact {
		return nil, errors.New("GitHub response incomplete or oversized")
	}
	return b, nil
}

func exactObject(b []byte, names ...string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if update.StrictJSON(b, &obj) != nil || obj == nil {
		return nil, errors.New("GitHub identity JSON invalid")
	}
	for k := range obj {
		for _, name := range names {
			if strings.EqualFold(k, name) && k != name {
				return nil, errors.New("GitHub identity field alias refused")
			}
		}
	}
	return obj, nil
}
func idJSON(b json.RawMessage) string {
	s := strings.TrimSpace(string(b))
	if !number(s) {
		return ""
	}
	return s
}
func stringJSON(b json.RawMessage) (string, error) {
	var s string
	err := json.Unmarshal(b, &s)
	return s, err
}

func (g *GitHubTransport) apiObject(ctx context.Context, relative string) (map[string]json.RawMessage, error) {
	g.metadata++
	if g.metadata > githubMaxMetadataFiles {
		return nil, errors.New("GitHub metadata budget exhausted")
	}
	r, err := g.request(ctx, "https://"+g.policy.APIHost+"/repos/"+g.policy.Repository+relative, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	b, err := githubRead(r, githubMaxMetadata, 0, true)
	if err != nil {
		return nil, err
	}
	return exactObject(b, "id", "full_name", "assets", "draft")
}

func (g *GitHubTransport) bindAsset(ctx context.Context, a GitHubAsset) error {
	if !g.repositoryChecked {
		repo, err := g.apiObject(ctx, "")
		if err != nil {
			return err
		}
		name, e := stringJSON(repo["full_name"])
		if e != nil || name != g.policy.Repository || idJSON(repo["id"]) != g.policy.RepositoryID {
			return errors.New("GitHub repository identity mismatch")
		}
		g.repositoryChecked = true
	}
	assets, ok := g.releases[a.ReleaseID]
	if !ok {
		release, err := g.apiObject(ctx, "/releases/"+a.ReleaseID)
		if err != nil {
			return err
		}
		if idJSON(release["id"]) != a.ReleaseID {
			return errors.New("GitHub release identity mismatch")
		}
		if draft, exists := release["draft"]; exists {
			var v bool
			if json.Unmarshal(draft, &v) != nil || v {
				return errors.New("GitHub release is not a public uploaded candidate")
			}
		}
		var list []json.RawMessage
		if json.Unmarshal(release["assets"], &list) != nil || len(list) == 0 || len(list) > 512 {
			return errors.New("GitHub release asset list invalid")
		}
		assets = map[string]githubAssetIdentity{}
		for _, raw := range list {
			obj, err := exactObject(raw, "id", "name", "size", "state")
			if err != nil {
				return err
			}
			id := idJSON(obj["id"])
			name, e := stringJSON(obj["name"])
			state, se := stringJSON(obj["state"])
			size, eSize := strconv.ParseInt(strings.TrimSpace(string(obj["size"])), 10, 64)
			if id == "" || e != nil || se != nil || eSize != nil || size < 0 {
				return errors.New("GitHub asset identity invalid")
			}
			if _, duplicate := assets[id]; duplicate {
				return errors.New("GitHub duplicate asset identity")
			}
			assets[id] = githubAssetIdentity{Name: name, Size: size, State: state}
		}
		g.releases[a.ReleaseID] = assets
	}
	v, ok := assets[a.AssetID]
	if !ok || v.Name != a.Name || v.Size != a.Size || v.State != "uploaded" {
		return errors.New("GitHub signed asset/release membership mismatch")
	}
	return nil
}

func (g *GitHubTransport) Asset(ctx context.Context, a GitHubAsset) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := a.Validate(g.policy); err != nil {
		return nil, err
	}
	g.assets++
	if g.assets > githubMaxAssets {
		return nil, errors.New("GitHub asset budget exhausted")
	}
	ctx, cancel, err := g.context(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if err = g.bindAsset(ctx, a); err != nil {
		return nil, err
	}
	u := "https://" + g.policy.APIHost + "/repos/" + g.policy.Repository + "/releases/assets/" + a.AssetID
	for hop := 0; hop < 2; hop++ {
		r, err := g.request(ctx, u, "application/octet-stream")
		if err != nil {
			return nil, err
		}
		if r.StatusCode == 301 || r.StatusCode == 302 || r.StatusCode == 303 || r.StatusCode == 307 || r.StatusCode == 308 {
			location := r.Header.Get("Location")
			r.Body.Close()
			if hop != 0 || strings.Contains(location, "#") {
				return nil, errors.New("GitHub redirect refused")
			}
			next, e := parsed(location, g.policy.CDNHost, a.CDNPath, true)
			if e != nil {
				return nil, errors.New("GitHub redirect refused")
			}
			query, decodeErr := url.QueryUnescape(next.RawQuery)
			if decodeErr != nil || next.ForceQuery || strings.ContainsAny(location, " \t\r\n\x00") {
				return nil, errors.New("GitHub redirect refused")
			}
			for _, c := range query {
				if unicode.IsControl(c) {
					return nil, errors.New("GitHub redirect refused")
				}
			}
			u = next.String()
			continue
		}
		b, err := githubRead(r, a.Size, a.Size, false)
		if err != nil {
			return nil, err
		}
		if sha(b) != a.SHA256 {
			return nil, errors.New("GitHub artifact hash mismatch")
		}
		return b, nil
	}
	return nil, errors.New("GitHub redirect budget exhausted")
}

// MetadataURL is a display-free policy helper; redirect query strings never
// enter a locator, report or persisted policy.
func (p GitHubPolicy) MetadataURL(relative string) (string, error) {
	if p.Validate() != nil {
		return "", errors.New("invalid GitHub policy")
	}
	if _, ok := metadataPath(relative); !ok {
		return "", errors.New("invalid GitHub metadata path")
	}
	u := &url.URL{Scheme: "https", Host: p.MetadataHost, Path: "/" + p.Repository + "/" + p.MetadataBranch + "/" + relative}
	return u.String(), nil
}
