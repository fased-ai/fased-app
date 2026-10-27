// Package privateaccess owns only a previously empty Tailscale Serve endpoint.
// It never installs Tailscale, enrolls a device, enables Funnel or changes SSH.
package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
)

type Runner func(context.Context, ...string) ([]byte, error)
type Manager struct {
	Run       Runner
	GetConfig func(context.Context) ([]byte, string, error)
	SetConfig func(context.Context, []byte, string) error
	Client    *http.Client
}
type Plan struct {
	Mode         string   `json:"mode"`
	Origin       string   `json:"origin"`
	Target       string   `json:"target"`
	Instructions []string `json:"instructions"`
}
type receipt struct {
	Schema int    `json:"schema"`
	Phase  string `json:"phase"`
	Plan   Plan   `json:"plan"`
}

func System() Manager {
	return Manager{GetConfig: systemConfig, SetConfig: systemSetConfig, Run: func(ctx context.Context, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		binary, err := exec.LookPath("tailscale")
		if err != nil {
			return nil, errors.New("install Tailscale from https://tailscale.com/download, authorize this device, then retry; SSH access remains available")
		}
		data, err := exec.CommandContext(ctx, binary, args...).Output()
		if err != nil {
			return nil, errors.New("Tailscale is not ready or this OS user lacks Serve permission; use tailscale status and the Tailscale owner settings, then retry")
		}
		if len(data) > 1024*1024 {
			return nil, errors.New("unexpected Tailscale response size")
		}
		return data, nil
	}}
}
func target(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || port == "0" {
		return "", errors.New("private access requires a fixed loopback listener")
	}
	if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
		return "", errors.New("invalid listener port")
	}
	return "http://" + listen, nil
}

var dnsName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*\.ts\.net$`)

func (m Manager) Plan(ctx context.Context, mode, listen string) (Plan, error) {
	u, err := target(listen)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Mode: mode, Target: u}
	switch mode {
	case "local":
		p.Origin = u
		p.Instructions = []string{"Open " + u + " on this computer. No Tailscale or RPC account is required."}
	case "ssh":
		host, port, _ := net.SplitHostPort(listen)
		if host != "127.0.0.1" {
			return p, errors.New("the SSH guide requires 127.0.0.1; use that listener on the VPS")
		}
		p.Origin = u
		p.Instructions = []string{"On your computer, replace USER@VPS with your normal SSH destination:", fmt.Sprintf("ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:%s:127.0.0.1:%s USER@VPS", port, port), "Keep that terminal open, then open " + u + " on your computer. Verify the SSH host key through your VPS console; never disable host-key checks.", "Claim/login is still required. No SSH or firewall settings are changed."}
	case "tailscale":
		data, err := m.Run(ctx, "status", "--json")
		if err != nil {
			return p, err
		}
		var status struct {
			BackendState string
			Self         struct {
				DNSName string
				Online  bool
			}
		}
		if json.Unmarshal(data, &status) != nil || status.BackendState != "Running" || !status.Self.Online {
			return p, errors.New("authorize and connect the VPS to your own tailnet first; no enrollment credential is stored by Fased")
		}
		dns := strings.TrimSuffix(status.Self.DNSName, ".")
		if !dnsName.MatchString(dns) {
			return p, errors.New("Tailscale must provide a valid MagicDNS ts.net name")
		}
		p.Origin = "https://" + dns
		p.Instructions = []string{"Authorize your computer and VPS into your own tailnet; enable tailnet HTTPS and restrict device/identity access in its policy.", "The installer configures only private Serve HTTPS on an unused port 443. Funnel is never enabled.", "Open " + p.Origin + " from your authorized computer, then claim/login. Keep normal SSH/console access as recovery."}
	default:
		return p, errors.New("access must be local, ssh or tailscale")
	}
	return p, nil
}
func (m Manager) config(ctx context.Context) (map[string]any, string, error) {
	data, etag, err := m.GetConfig(ctx)
	if err != nil {
		return nil, "", err
	}
	if etag == "" {
		return nil, "", errors.New("Tailscale did not provide conditional-write identity; no mutation is permitted")
	}
	var c map[string]any
	if json.Unmarshal(data, &c) != nil {
		return nil, "", errors.New("invalid Serve configuration; refusing mutation")
	}
	// Some versions encode an unused optional section as null or an empty map.
	for k, v := range c {
		if v == nil {
			delete(c, k)
		} else if x, ok := v.(map[string]any); ok && len(x) == 0 {
			delete(c, k)
		}
	}
	return c, etag, nil
}
func expected(p Plan) map[string]any {
	host := strings.TrimPrefix(p.Origin, "https://") + ":443"
	return map[string]any{"TCP": map[string]any{"443": map[string]any{"HTTPS": true}}, "Web": map[string]any{host: map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": p.Target}}}}}
}
func load(root string) (receipt, error) {
	var r receipt
	data, err := core.ReadPrivateFile(filepath.Join(root, "private-access.json"))
	if err != nil {
		return r, err
	}
	if json.Unmarshal(data, &r) != nil || r.Schema != 1 || r.Plan.Mode != "tailscale" || (r.Phase != "preparing" && r.Phase != "ready") || !dnsName.MatchString(strings.TrimPrefix(r.Plan.Origin, "https://")) {
		return r, errors.New("unknown private-access receipt; preserve it for recovery")
	}
	if !strings.HasPrefix(r.Plan.Target, "http://") {
		return r, errors.New("invalid private target")
	}
	if _, err = target(strings.TrimPrefix(r.Plan.Target, "http://")); err != nil {
		return r, err
	}
	return r, nil
}
func (m Manager) Enable(ctx context.Context, root string, p Plan, instanceID string) error {
	if p.Mode != "tailscale" {
		return nil
	}
	if !dnsName.MatchString(strings.TrimPrefix(p.Origin, "https://")) {
		return errors.New("invalid private origin")
	}
	if err := core.EnsurePrivateDirectory(root); err != nil {
		return err
	}
	lock, err := core.LockInstance(filepath.Join(root, "access.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	cfg, etag, err := m.config(ctx)
	if err != nil {
		return err
	}
	r, err := load(root)
	if errors.Is(err, os.ErrNotExist) {
		if len(cfg) != 0 {
			return errors.New("existing Serve/Funnel configuration belongs to the owner; refusing to replace or adopt it")
		}
		r = receipt{Schema: 1, Phase: "preparing", Plan: p}
		if err = core.WritePrivateJSON(filepath.Join(root, "private-access.json"), r); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if r.Plan.Origin != p.Origin || r.Plan.Target != p.Target {
		return errors.New("private-access identity changed; recover the existing route first")
	}
	if len(cfg) == 0 {
		next, _ := json.Marshal(expected(p))
		if err = m.SetConfig(ctx, next, etag); err != nil {
			return err
		}
		cfg, _, err = m.config(ctx)
		if err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(cfg, expected(p)) {
		return errors.New("Serve/Funnel configuration differs; no reset or overwrite was attempted")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("private access redirected") }}
	if m.Client != nil {
		client = m.Client
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Origin+"/api/v1/health", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("private HTTPS could not be verified; retain SSH/console access and retry this command")
	}
	defer resp.Body.Close()
	if _, err = connection.ReadHealth(resp, instanceID); err != nil {
		return errors.New("private HTTPS reached a different or incompatible app; route remains recorded for recovery")
	}
	r.Phase = "ready"
	return core.WritePrivateJSON(filepath.Join(root, "private-access.json"), r)
}
func (m Manager) Remove(ctx context.Context, root string) error {
	r, err := load(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lock, err := core.LockInstance(filepath.Join(root, "access.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	cfg, etag, err := m.config(ctx)
	if err != nil {
		return err
	}
	if len(cfg) != 0 {
		if !reflect.DeepEqual(cfg, expected(r.Plan)) {
			return errors.New("private route changed or has other owner routes; refusing removal")
		}
		if err = m.SetConfig(ctx, []byte("{}"), etag); err != nil {
			return err
		}
		cfg, _, err = m.config(ctx)
		if err != nil || len(cfg) != 0 {
			return errors.New("route removal is not confirmed; retry after inspection")
		}
	}
	return os.Remove(filepath.Join(root, "private-access.json"))
}
