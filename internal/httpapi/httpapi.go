package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// SetSecurityHeaders applies Fased's default strict browser CSP, isolation, and framing headers.
func SetSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
}

// IsLoopbackHost reports whether host parses as a loopback IP literal.
func IsLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidatePublicOrigin validates an HTTPS public origin without credentials, path, query, or fragment.
func ValidatePublicOrigin(publicOrigin string) (string, string, error) {
	parsed, err := url.Parse(publicOrigin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("public origin must be an exact HTTPS origin without credentials or a path")
	}
	return strings.TrimSuffix(publicOrigin, "/"), parsed.Host, nil
}

// WriteJSON encodes value as UTF-8 application/json with HTTP status code.
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// WriteError writes a JSON {"error": message} response body.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]string{"error": message})
}

// DecodeStrictJSON enforces maxBytes body bounds, rejects unknown JSON fields, and rejects trailing tokens.
func DecodeStrictJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, "one JSON object required")
		return false
	}
	return true
}

// ServeStaticAsset serves read-only embedded SPA assets with no-store on index.html and immutable caching on hashed assets.
func ServeStaticAsset(w http.ResponseWriter, r *http.Request, assets fs.FS) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if assets == nil {
		WriteError(w, http.StatusServiceUnavailable, "UI assets unavailable")
		return
	}
	clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if clean == "" || !strings.Contains(path.Base(clean), ".") {
		clean = "index.html"
	}
	if !fs.ValidPath(clean) {
		WriteError(w, http.StatusNotFound, "not found")
		return
	}
	data, err := fs.ReadFile(assets, clean)
	if err != nil {
		WriteError(w, http.StatusNotFound, "not found")
		return
	}
	kind := mime.TypeByExtension(path.Ext(clean))
	if kind != "" {
		w.Header().Set("Content-Type", kind)
	}
	if clean == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}
