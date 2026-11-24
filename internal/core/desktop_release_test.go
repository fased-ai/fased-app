package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func sampleDesktopManifestJSON(t *testing.T, version string, signed bool) []byte {
	t.Helper()
	tag := "v" + version + "-desktop"
	base := "https://github.com/fased-ai/fased-app/releases/download/" + tag + "/"
	m := update.DesktopManifest{
		SchemaVersion: 1,
		Version:       version,
		ReleaseTag:    tag,
		Source:        strings.Repeat("a", 40),
		Signed:        signed,
		Assets: map[string]update.DesktopAsset{
			"fased-app-" + version + "-darwin-arm64.dmg": {
				URL:    base + "fased-app-" + version + "-darwin-arm64.dmg",
				SHA256: strings.Repeat("b", 64),
				Bytes:  42000000,
				OS:     "darwin",
				Arch:   "arm64",
				Kind:   "desktop",
			},
			"fased-cli-" + version + "-darwin-arm64.tar.gz": {
				URL:    base + "fased-cli-" + version + "-darwin-arm64.tar.gz",
				SHA256: strings.Repeat("c", 64),
				Bytes:  21000000,
				OS:     "darwin",
				Arch:   "arm64",
				Kind:   "cli",
			},
			"fased-app-" + version + "-linux-amd64.deb": {
				URL:    base + "fased-app-" + version + "-linux-amd64.deb",
				SHA256: strings.Repeat("d", 64),
				Bytes:  51000000,
				OS:     "linux",
				Arch:   "amd64",
				Kind:   "desktop",
			},
			"install-cli.sh": {
				URL:    base + "install-cli.sh",
				SHA256: strings.Repeat("e", 64),
				Bytes:  10779,
				OS:     "all",
				Arch:   "all",
				Kind:   "installer",
			},
			"SHA256SUMS.txt": {
				URL:    base + "SHA256SUMS.txt",
				SHA256: strings.Repeat("f", 64),
				Bytes:  1200,
				OS:     "all",
				Arch:   "all",
				Kind:   "checksums",
			},
		},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFetchPublicDesktopReleaseAndFiltering(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/fased-ai/fased-app/releases/latest":
			http.Redirect(w, r, "/fased-ai/fased-app/releases/tag/v0.1.37", http.StatusFound)
		case "/fased-ai/fased-app/releases/download/v0.1.37-desktop/desktop-release-manifest.json":
			http.Redirect(w, r, "/cdn/v0.1.37-desktop/desktop-release-manifest.json", http.StatusFound)
		case "/cdn/v0.1.37-desktop/desktop-release-manifest.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(sampleDesktopManifestJSON(t, "0.1.37", false))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	now := time.Unix(1795400000, 0)
	latestURL := srv.URL + "/fased-ai/fased-app/releases/latest"
	downloadBase := srv.URL + "/fased-ai/fased-app/releases/download/"

	view, err := fetchPublicDesktopRelease(t.Context(), srv.Client().Transport, latestURL, downloadBase, "0.1.36", "darwin", "arm64", now)
	if err != nil {
		t.Fatalf("fetchPublicDesktopRelease: %v", err)
	}
	if !view.UpdateAvailable || view.LatestVersion != "0.1.37" || view.ReleaseTag != "v0.1.37-desktop" || view.Signed {
		t.Fatalf("unexpected view: %+v", view)
	}
	if view.ReleaseURL != "https://github.com/fased-ai/fased-app/releases/tag/v0.1.37-desktop" {
		t.Fatalf("unexpected release URL: %q", view.ReleaseURL)
	}
	if len(view.Assets) != 4 {
		t.Fatalf("expected 4 darwin/arm64+all assets, got %d: %+v", len(view.Assets), view.Assets)
	}
	if view.Assets[0].Kind != "desktop" || view.Assets[1].Kind != "cli" || view.Assets[2].Kind != "installer" || view.Assets[3].Kind != "checksums" {
		t.Fatalf("unexpected asset sort order: %+v", view.Assets)
	}

	same, err := fetchPublicDesktopRelease(t.Context(), srv.Client().Transport, latestURL, downloadBase, "0.1.37", "linux", "amd64", now)
	if err != nil {
		t.Fatalf("same-version check: %v", err)
	}
	if same.UpdateAvailable {
		t.Fatalf("expected updateAvailable=false when installed equals latest: %+v", same)
	}

	devView, err := fetchPublicDesktopRelease(t.Context(), srv.Client().Transport, latestURL, downloadBase, "0.0.0-core.1", "linux", "amd64", now)
	if err != nil || !devView.UpdateAvailable {
		t.Fatalf("expected dev build to treat 0.1.37 as newer, got view=%+v err=%v", devView, err)
	}
}

func TestFetchPublicDesktopReleaseRejectsUntrustedRedirectAndOversize(t *testing.T) {
	t.Parallel()
	untrustedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://attacker.example/evil", http.StatusFound)
	}))
	defer untrustedSrv.Close()
	if _, err := fetchPublicDesktopRelease(t.Context(), untrustedSrv.Client().Transport, untrustedSrv.URL+"/fased-ai/fased-app/releases/latest", untrustedSrv.URL+"/dl/", "0.1.36", "linux", "amd64", time.Now()); err == nil {
		t.Fatal("expected untrusted latest redirect origin to fail")
	}

	untrustedDownloadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fased-ai/fased-app/releases/latest":
			http.Redirect(w, r, "/fased-ai/fased-app/releases/tag/v0.1.37", http.StatusFound)
		default:
			http.Redirect(w, r, "https://attacker.example/manifest.json", http.StatusFound)
		}
	}))
	defer untrustedDownloadSrv.Close()
	if _, err := fetchPublicDesktopRelease(t.Context(), untrustedDownloadSrv.Client().Transport, untrustedDownloadSrv.URL+"/fased-ai/fased-app/releases/latest", untrustedDownloadSrv.URL+"/dl/", "0.1.36", "linux", "amd64", time.Now()); err == nil {
		t.Fatal("expected untrusted manifest redirect host to fail")
	}

	oversizeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fased-ai/fased-app/releases/latest":
			http.Redirect(w, r, "/fased-ai/fased-app/releases/tag/v0.1.37", http.StatusFound)
		default:
			_, _ = w.Write([]byte(strings.Repeat("a", update.MaxDesktopDocument+32)))
		}
	}))
	defer oversizeSrv.Close()
	if _, err := fetchPublicDesktopRelease(t.Context(), oversizeSrv.Client().Transport, oversizeSrv.URL+"/fased-ai/fased-app/releases/latest", oversizeSrv.URL+"/dl/", "0.1.36", "linux", "amd64", time.Now()); err == nil {
		t.Fatal("expected oversized manifest to fail")
	}
}

func TestDesktopReleaseHTTPEndpointAuthAndCache(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fased-ai/fased-app/releases/latest":
			calls.Add(1)
			http.Redirect(w, r, "/fased-ai/fased-app/releases/tag/v0.1.37", http.StatusFound)
		case "/dl/v0.1.37-desktop/desktop-release-manifest.json":
			_, _ = w.Write(sampleDesktopManifestJSON(t, "0.1.37", false))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	now := time.Unix(1795400000, 0)
	h.desktopRelease.transport = srv.Client().Transport
	h.desktopRelease.latestURL = srv.URL + "/fased-ai/fased-app/releases/latest"
	h.desktopRelease.downloadBase = srv.URL + "/dl/"
	h.desktopRelease.now = func() time.Time { return now }

	unauth := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/updates/desktop-release", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, unauth)
	if w.Code != 401 {
		t.Fatalf("expected 401 unauthenticated, got %d", w.Code)
	}

	postReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/updates/desktop-release", nil)
	postReq.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, postReq)
	if w.Code != 405 {
		t.Fatalf("expected 405 POST, got %d", w.Code)
	}

	queryReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/updates/desktop-release?foo=1", nil)
	queryReq.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, queryReq)
	if w.Code != 400 {
		t.Fatalf("expected 400 query param, got %d", w.Code)
	}

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/updates/desktop-release", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("request %d failed: %d %s", i, w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 60s cache to collapse second request, got %d fetches", calls.Load())
	}

	// Verify 404 when manifest is missing does not get cached and returns 404.
	h2, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	h2.desktopRelease.fetcher = func(context.Context) (DesktopReleaseCheckView, error) {
		return DesktopReleaseCheckView{}, errDesktopManifestNotPublished
	}
	req404 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/updates/desktop-release", nil)
	req404.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h2.ServeHTTP(w, req404)
	if w.Code != 404 {
		t.Fatalf("expected 404 when manifest unpublished, got %d", w.Code)
	}
}
