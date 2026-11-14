package privateaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

// The Serve LocalAPI is version-sensitive. Require its ETag/If-Match contract,
// preserve an exact endpoint inventory and fail closed on unknown responses.
// https://github.com/tailscale/tailscale/blob/main/client/local/serve.go
func localRequest(ctx context.Context, method string, data []byte, etag string) ([]byte, string, error) {
	if runtime.GOOS != "linux" {
		return nil, "", errors.New("guided Tailscale Serve configuration currently targets Linux VPS only")
	}
	t := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/tailscale/tailscaled.sock")
	}}
	defer t.CloseIdleConnections()
	c := &http.Client{Transport: t, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("local daemon redirected") }}
	r, e := http.NewRequestWithContext(ctx, method, "http://local-tailscaled.sock/localapi/v0/serve-config", bytes.NewReader(data))
	if e != nil {
		return nil, "", e
	}
	r.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost {
		if etag == "" {
			return nil, "", errors.New("missing conditional identity")
		}
		r.Header.Set("If-Match", etag)
	}
	resp, e := c.Do(r)
	if e != nil {
		return nil, "", errors.New("Tailscale local daemon unavailable; use SSH access or check its installation")
	}
	return readServeResponse(resp, method)
}

// readServeResponse keeps response classification independent of the local socket.
func readServeResponse(resp *http.Response, method string) ([]byte, string, error) {
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return nil, "", servePermissionError{}
	}
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
		if err != nil || len(body) > 4096 {
			return nil, "", fmt.Errorf("invalid Tailscale Serve refusal (HTTP %d); preserve its configuration", resp.StatusCode)
		}
		// Linux operators can write ordinary Serve settings but Unix ingress
		// additionally requires local-admin authority. Tailscale 1.104.5
		// refuses that operation with this specific401 before any mutation.
		// Unrelated401s must not trigger the administrator's fixed OS action.
		if method == http.MethodPost && resp.StatusCode == http.StatusUnauthorized && strings.TrimSpace(string(body)) == serveUnixAdminRefusal {
			return nil, "", servePermissionError{}
		}
		// Keep the status for diagnosis without disclosing an arbitrary daemon
		// body (which may contain credentials or private configuration).
		return nil, "", fmt.Errorf("Tailscale Serve request refused (HTTP %d); preserve its configuration and inspect the daemon response before retry", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if e != nil || len(b) > 1024*1024 {
		return nil, "", errors.New("invalid daemon response")
	}
	return b, resp.Header.Get("ETag"), nil
}

// WithServeAuthorization is reserved for an administrator's setup/lifecycle
// command. Ordinary runtime has no elevated transport.
func (m Manager) WithServeAuthorization(authorize func(context.Context, []byte, string) error) Manager {
	direct := m.SetConfig
	m.SetConfig = func(ctx context.Context, data []byte, etag string) error {
		err := direct(ctx, data, etag)
		var denied servePermissionError
		if !errors.As(err, &denied) || authorize == nil {
			return err
		}
		if _, e := serveCommand(data, etag); e != nil {
			if m.GetConfig == nil {
				return e
			}
			if _, e = serveRawCommand(data, etag); e != nil {
				return e
			}
		}
		// Only a definite permission refusal (403 or the recognized Unix401)
		// proves this attempt did not commit. A timeout, conflict or other
		// error must reconcile through the saved receipt.
		return authorize(ctx, data, etag)
	}
	return m
}

type servePermissionError struct{}

func (servePermissionError) Error() string { return "Tailscale Serve OS authorization required" }

const serveUnixAdminRefusal = "must be root, or be an operator and able to run 'sudo tailscale' to serve a path or Unix socket"

func systemConfig(ctx context.Context) ([]byte, string, error) {
	return localRequest(ctx, http.MethodGet, nil, "")
}
func systemSetConfig(ctx context.Context, b []byte, etag string) error {
	_, _, e := localRequest(ctx, http.MethodPost, b, etag)
	return e
}

// No filesystem path, URL, proxy, curl config or response file comes from the
// caller. Only the owned Serve body and its daemon-issued conditional identity
// cross the setup OS boundary; neither is a credential.
var serveCurlPrefix = []string{
	"/usr/bin/curl", "--disable", "--silent", "--show-error", "--fail",
	"--max-time", "15", "--connect-timeout", "5", "--max-filesize", "1048576",
	"--noproxy", "*", "--proto", "=http", "--unix-socket", "/var/run/tailscale/tailscaled.sock",
	"--request", "POST", "--header", "Content-Type: application/json", "--header",
}
var serveETag = regexp.MustCompile(`^[a-f0-9]{64}$`)
var servePublicSocket = regexp.MustCompile(`^unix:(/var/lib/fased-app-(vps|custody)-[1-9][0-9]*/app-state|/tmp/fased-private-[0-9]{1,10})/tailscale[.]sock$`)

// Bound requests are visible to sudo policy and local process inspection.
// Admit only Fased's credential-free Unix ingress records; preserve and refuse
// any other existing Serve configuration rather than disclose its contents.
func serveArgumentSafe(cfg map[string]any) bool {
	tcp, web, err := inventory(cfg)
	if err != nil || len(tcp) != len(web) {
		return false
	}
	for hostport, entry := range web {
		host, port, err := net.SplitHostPort(hostport)
		if err != nil {
			return false
		}
		origin := "https://" + host
		if port != "443" {
			origin += ":" + port
		}
		value, ok := entry.(map[string]any)
		if !ok {
			return false
		}
		handlers, ok := value["Handlers"].(map[string]any)
		if !ok {
			return false
		}
		root, ok := handlers["/"].(map[string]any)
		if !ok {
			return false
		}
		target, ok := root["Proxy"].(string)
		plan := Plan{Mode: "tailscale", Origin: origin, Target: target}
		if !ok || !servePublicSocket.MatchString(target) {
			return false
		}
		if _, _, err := routeParts(plan); err != nil {
			return false
		}
		want := expected(plan)
		if !reflect.DeepEqual(entry, want["Web"].(map[string]any)[hostport]) || !reflect.DeepEqual(tcp[port], want["TCP"].(map[string]any)[port]) {
			return false
		}
	}
	return true
}

func serveCommand(data []byte, etag string) ([]string, error) {
	if !serveETag.MatchString(etag) || len(data) > 16*1024 {
		return nil, errors.New("invalid private Serve conditional request")
	}
	var cfg map[string]any
	if json.Unmarshal(data, &cfg) != nil || cfg == nil {
		return nil, errors.New("invalid private Serve configuration")
	}
	if len(cfg) > 0 {
		// Admit exactly one HTTPS443 / handler to the dedicated Unix ingress.
		web, ok := cfg["Web"].(map[string]any)
		if !ok || len(web) != 1 {
			return nil, errors.New("only the owned private Serve endpoint may be authorized")
		}
		var plan Plan
		for host, entry := range web {
			if !strings.HasSuffix(host, ":443") {
				return nil, errors.New("private Serve port changed")
			}
			plan.Origin = "https://" + strings.TrimSuffix(host, ":443")
			value, ok := entry.(map[string]any)
			if !ok {
				return nil, errors.New("invalid private Serve handlers")
			}
			handlers, ok := value["Handlers"].(map[string]any)
			if !ok {
				return nil, errors.New("invalid private Serve handlers")
			}
			root, ok := handlers["/"].(map[string]any)
			if !ok {
				return nil, errors.New("invalid private Serve root")
			}
			plan.Target, _ = root["Proxy"].(string)
		}
		if !dnsName.MatchString(strings.TrimPrefix(plan.Origin, "https://")) || !strings.HasPrefix(plan.Target, "unix:") || !validTarget(plan.Target) || !reflect.DeepEqual(cfg, expected(plan)) {
			return nil, errors.New("only the owned private Unix Serve endpoint may be authorized")
		}
	}
	return serveRawCommand(data, etag)
}

// The OS runner must additionally compare this body against the daemon's
// current ETag and the caller's recorded route before admitting a write.
func serveRawCommand(data []byte, etag string) ([]string, error) {
	if !serveETag.MatchString(etag) || len(data) > 16*1024 {
		return nil, errors.New("invalid private Serve conditional request")
	}
	var compact bytes.Buffer
	if json.Compact(&compact, data) != nil || !bytes.Equal(compact.Bytes(), data) {
		return nil, errors.New("private Serve request must be compact JSON")
	}
	var cfg map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&cfg) != nil || cfg == nil || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid private Serve configuration")
	}
	if !serveArgumentSafe(cfg) {
		return nil, errors.New("private Serve configuration cannot be safely bound; preserve it")
	}
	args := append([]string(nil), serveCurlPrefix...)
	return append(args, "If-Match: "+etag, "--data-raw", string(data), "--write-out", "%{http_code}", "http://local-tailscaled.sock/localapi/v0/serve-config"), nil
}

// AllowedServeCommand is also the OS runner's allowlist. It is deliberately not
// a general elevated curl or Tailscale client.
func AllowedServeCommand(input []byte, args []string) bool {
	if len(args) != len(serveCurlPrefix)+6 {
		return false
	}
	header := args[len(serveCurlPrefix)]
	if !strings.HasPrefix(header, "If-Match: ") {
		return false
	}
	expected, err := serveCommand(input, strings.TrimPrefix(header, "If-Match: "))
	return err == nil && slices.Equal(args, expected)
}

func SetServeWithOS(ctx context.Context, data []byte, etag string, run func(context.Context, []byte, ...string) ([]byte, error)) error {
	args, err := serveRawCommand(data, etag)
	if err != nil {
		return err
	}
	response, err := run(ctx, data, args...)
	if err != nil {
		return fmt.Errorf("private Serve OS setup did not complete; preserve its receipt and retry the same setup: %w", err)
	}
	if len(response) > 1024*1024+3 || !bytes.HasSuffix(response, []byte("200")) {
		return errors.New("private Serve OS response unconfirmed; preserve its receipt before retry")
	}
	return nil
}
