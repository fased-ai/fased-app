package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

const (
	defaultDesktopReleasesLatestURL   = "https://github.com/fased-ai/fased-app/releases/latest"
	defaultDesktopReleaseDownloadBase = "https://github.com/fased-ai/fased-app/releases/download/"
	defaultDesktopReleaseTagURLBase   = "https://github.com/fased-ai/fased-app/releases/tag/"
	desktopReleaseLatestPathPrefix    = "/fased-ai/fased-app/releases/tag/"
	desktopReleaseCacheTTL            = 60 * time.Second
	desktopReleaseRequestTimeout      = 12 * time.Second
)

var errDesktopManifestNotPublished = errors.New("desktop release manifest not published")

type DesktopReleaseAssetView struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type DesktopReleaseCheckView struct {
	InstalledVersion string                    `json:"installedVersion"`
	LatestVersion    string                    `json:"latestVersion"`
	ReleaseTag       string                    `json:"releaseTag"`
	ReleaseURL       string                    `json:"releaseUrl"`
	UpdateAvailable  bool                      `json:"updateAvailable"`
	Signed           bool                      `json:"signed"`
	Platform         string                    `json:"platform"`
	Arch             string                    `json:"arch"`
	Assets           []DesktopReleaseAssetView `json:"assets"`
	CheckedAt        int64                     `json:"checkedAt"`
}

type desktopReleaseCache struct {
	mu           sync.Mutex
	cached       *DesktopReleaseCheckView
	cachedAt     time.Time
	now          func() time.Time
	transport    http.RoundTripper
	latestURL    string
	downloadBase string
	fetcher      func(context.Context) (DesktopReleaseCheckView, error)
}

func (c *desktopReleaseCache) clockNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (h *HTTPHandler) desktopReleaseAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, "unexpected query parameters")
		return
	}
	view, err := h.checkDesktopRelease(r.Context())
	if errors.Is(err, errDesktopManifestNotPublished) {
		writeError(w, 404, "Desktop release manifest is not published yet.")
		return
	}
	if err != nil {
		writeError(w, 502, "Could not check desktop release feed.")
		return
	}
	writeJSON(w, 200, view)
}

func (h *HTTPHandler) checkDesktopRelease(ctx context.Context) (DesktopReleaseCheckView, error) {
	now := h.desktopRelease.clockNow()
	h.desktopRelease.mu.Lock()
	if h.desktopRelease.cached != nil && now.Sub(h.desktopRelease.cachedAt) < desktopReleaseCacheTTL {
		out := *h.desktopRelease.cached
		out.Assets = append([]DesktopReleaseAssetView(nil), h.desktopRelease.cached.Assets...)
		h.desktopRelease.mu.Unlock()
		return out, nil
	}
	fetcher := h.desktopRelease.fetcher
	rt := h.desktopRelease.transport
	latestURL := h.desktopRelease.latestURL
	downloadBase := h.desktopRelease.downloadBase
	h.desktopRelease.mu.Unlock()

	var (
		view DesktopReleaseCheckView
		err  error
	)
	if fetcher != nil {
		view, err = fetcher(ctx)
	} else {
		view, err = fetchPublicDesktopRelease(ctx, rt, latestURL, downloadBase, Version, runtime.GOOS, runtime.GOARCH, now)
	}
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	h.desktopRelease.mu.Lock()
	copyView := view
	copyView.Assets = append([]DesktopReleaseAssetView(nil), view.Assets...)
	h.desktopRelease.cached = &copyView
	h.desktopRelease.cachedAt = h.desktopRelease.clockNow()
	h.desktopRelease.mu.Unlock()
	return view, nil
}

func fetchPublicDesktopRelease(
	ctx context.Context,
	rt http.RoundTripper,
	latestURL, downloadBase, installedVersion, targetOS, targetArch string,
	checkedAt time.Time,
) (DesktopReleaseCheckView, error) {
	if rt == nil {
		rt = newPublicNewsTransport()
	}
	if latestURL == "" {
		latestURL = defaultDesktopReleasesLatestURL
	}
	if downloadBase == "" {
		downloadBase = defaultDesktopReleaseDownloadBase
	}
	baseParsed, err := url.Parse(latestURL)
	if err != nil || baseParsed.Scheme == "" || baseParsed.Host == "" {
		return DesktopReleaseCheckView{}, errors.New("invalid desktop latest URL")
	}

	reqCtx, cancel := context.WithTimeout(ctx, desktopReleaseRequestTimeout)
	defer cancel()

	noRedirectClient := &http.Client{
		Transport: rt,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, latestURL, nil)
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	req.Header.Set("User-Agent", "fased-app")
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return DesktopReleaseCheckView{}, errors.New("unexpected latest release redirect status")
	}
	_, latestVersion, err := latestTagFromRedirectURL(baseParsed, resp.Header.Get("Location"))
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	desktopTag := "v" + latestVersion + "-desktop"
	manifestURL := strings.TrimRight(downloadBase, "/") + "/" + desktopTag + "/desktop-release-manifest.json"

	downloadClient := &http.Client{
		Transport: rt,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if !allowedDesktopDownloadURL(next.URL, baseParsed) {
				return errors.New("untrusted desktop manifest redirect target")
			}
			return nil
		},
	}
	mReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	mReq.Header.Set("User-Agent", "fased-app")
	mReq.Header.Set("Accept", "application/json")
	mResp, err := downloadClient.Do(mReq)
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	defer mResp.Body.Close()

	if mResp.StatusCode == http.StatusNotFound {
		return DesktopReleaseCheckView{}, errDesktopManifestNotPublished
	}
	if mResp.StatusCode != http.StatusOK {
		return DesktopReleaseCheckView{}, errors.New("unexpected desktop manifest HTTP status")
	}
	raw, err := io.ReadAll(io.LimitReader(mResp.Body, update.MaxDesktopDocument+1))
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	if len(raw) > update.MaxDesktopDocument {
		return DesktopReleaseCheckView{}, errors.New("desktop manifest exceeds size limit")
	}
	manifest, err := update.ParseDesktopManifest(raw)
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	if manifest.Version != latestVersion || manifest.ReleaseTag != desktopTag {
		return DesktopReleaseCheckView{}, errors.New("desktop manifest version mismatch")
	}
	updateAvailable, err := compareInstalledDesktopVersion(installedVersion, manifest)
	if err != nil {
		return DesktopReleaseCheckView{}, err
	}
	assets := selectDesktopReleaseAssets(manifest, targetOS, targetArch)
	return DesktopReleaseCheckView{
		InstalledVersion: installedVersion,
		LatestVersion:    manifest.Version,
		ReleaseTag:       manifest.ReleaseTag,
		ReleaseURL:       defaultDesktopReleaseTagURLBase + manifest.ReleaseTag,
		UpdateAvailable:  updateAvailable,
		Signed:           manifest.Signed,
		Platform:         targetOS,
		Arch:             targetArch,
		Assets:           assets,
		CheckedAt:        checkedAt.Unix(),
	}, nil
}

func latestTagFromRedirectURL(base *url.URL, location string) (string, string, error) {
	trimmed := strings.TrimSpace(location)
	if trimmed == "" || len(trimmed) > 1024 {
		return "", "", errors.New("missing redirect location")
	}
	loc, err := url.Parse(trimmed)
	if err != nil {
		return "", "", err
	}
	resolved := base.ResolveReference(loc)
	if resolved.Scheme != base.Scheme || !strings.EqualFold(resolved.Host, base.Host) || resolved.User != nil {
		return "", "", errors.New("untrusted latest release redirect origin")
	}
	if !strings.HasPrefix(resolved.Path, desktopReleaseLatestPathPrefix) {
		return "", "", errors.New("unexpected latest release tag path")
	}
	tag := strings.TrimPrefix(resolved.Path, desktopReleaseLatestPathPrefix)
	if !strings.HasPrefix(tag, "v") {
		return "", "", errors.New("canonical vX.Y.Z release tag required")
	}
	ver := strings.TrimPrefix(tag, "v")
	if !canonicalSemverTriplet(ver) {
		return "", "", errors.New("canonical vX.Y.Z release tag required")
	}
	return tag, ver, nil
}

func canonicalSemverTriplet(raw string) bool {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		v, err := strconv.ParseUint(part, 10, 20)
		if err != nil || v >= 1000000 || strconv.FormatUint(v, 10) != part {
			return false
		}
	}
	return true
}

func allowedDesktopDownloadURL(u, base *url.URL) bool {
	if u == nil || u.User != nil {
		return false
	}
	if u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) {
		return true
	}
	if u.Scheme != "https" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return host == "github.com" || host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

func compareInstalledDesktopVersion(installedVersion string, manifest update.DesktopManifest) (bool, error) {
	avail, err := update.DesktopUpdateAvailable(installedVersion, manifest)
	if err == nil {
		return avail, nil
	}
	trimmed := strings.TrimPrefix(strings.TrimSpace(installedVersion), "v")
	if base, _, ok := strings.Cut(trimmed, "-"); ok && canonicalSemverTriplet(base) {
		return update.DesktopUpdateAvailable(base, manifest)
	}
	return false, err
}

func desktopAssetKindPriority(kind string) int {
	switch kind {
	case "desktop":
		return 0
	case "cli":
		return 1
	case "installer":
		return 2
	case "checksums":
		return 3
	default:
		return 4
	}
}

func selectDesktopReleaseAssets(manifest update.DesktopManifest, targetOS, targetArch string) []DesktopReleaseAssetView {
	out := make([]DesktopReleaseAssetView, 0, len(manifest.Assets))
	for name, asset := range manifest.Assets {
		if (asset.OS == targetOS || asset.OS == "all") && (asset.Arch == targetArch || asset.Arch == "all") {
			out = append(out, DesktopReleaseAssetView{
				Name:   name,
				Kind:   asset.Kind,
				OS:     asset.OS,
				Arch:   asset.Arch,
				URL:    asset.URL,
				SHA256: asset.SHA256,
				Bytes:  asset.Bytes,
			})
		}
	}
	slices.SortFunc(out, func(a, b DesktopReleaseAssetView) int {
		pa, pb := desktopAssetKindPriority(a.Kind), desktopAssetKindPriority(b.Kind)
		if pa != pb {
			return pa - pb
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}
