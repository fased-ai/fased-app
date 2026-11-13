package privateaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
)

// Route operations modify only one exclusive HTTPS port. Other Serve entries
// are preserved; unknown top-level policy or any Funnel setting fails closed.
func routeParts(p Plan) (string, string, error) {
	if p.Mode != "tailscale" || !validTarget(p.Target) {
		return "", "", errors.New("invalid private route")
	}
	host, port, err := tailnet.Origin(p.Origin)
	if err != nil {
		return "", "", err
	}
	return net.JoinHostPort(host, port), port, nil
}

// SelectIsolated chooses an unused private HTTPS port before deployment. A
// retained setup receipt fixes the port on retry; an existing installation is
// never adopted as a new one.
func (m Manager) SelectIsolated(ctx context.Context, dir string, p Plan, protectedOrigin ...string) (Plan, error) {
	if len(protectedOrigin) > 1 {
		return p, errors.New("one protected private setup origin required")
	}
	protected := ""
	if len(protectedOrigin) == 1 {
		protected = protectedOrigin[0]
	}
	base, _, err := tailnet.Origin(p.Origin)
	if err != nil || p.Mode != "tailscale" || !validTarget(p.Target) {
		return p, errors.New("valid private setup required")
	}
	if _, err = load(dir); err == nil {
		return p, errors.New("this owner already has a private installation; preserve it")
	} else if !os.IsNotExist(err) {
		return p, err
	}
	file := filepath.Join(dir, "private-setup.json")
	if data, e := core.ReadPrivateFile(file); e == nil {
		var saved preflightReceipt
		if json.Unmarshal(data, &saved) != nil || saved.Version != 1 || saved.Plan.Mode != "tailscale" || !validTarget(saved.Plan.Target) || !strings.HasPrefix(saved.Plan.Target, "unix:/tmp/fased-private-") {
			return p, errors.New("unknown private setup receipt; preserve it")
		}
		host, _, e := tailnet.Origin(saved.Plan.Origin)
		if e != nil || host != base {
			return p, errors.New("private setup device changed; preserve its receipt")
		}
		if protected != "" && protected != saved.Plan.Origin {
			return p, errors.New("private setup origin differs from protected operation")
		}
		return BindOrigin(p, saved.Plan.Origin)
	} else if !os.IsNotExist(e) {
		return p, e
	}
	if protected != "" {
		return BindOrigin(p, protected)
	}
	cfg, _, err := m.config(ctx)
	if err != nil {
		return p, err
	}
	if _, _, err = inventory(cfg); err != nil {
		return p, err
	}
	old := p.Origin
	for index := -1; index < 100; index++ {
		candidate := "https://" + base
		if index >= 0 {
			candidate = fmt.Sprintf("https://%s:%d", base, 8443+index)
		}
		p.Origin = candidate
		state, e := routeState(cfg, p)
		if e == nil && state == "absent" {
			for i := range p.Instructions {
				p.Instructions[i] = strings.ReplaceAll(p.Instructions[i], old, candidate)
				if candidate != old {
					p.Instructions[i] = strings.ReplaceAll(p.Instructions[i], "an unused port 443", "a separate unused HTTPS port")
				}
			}
			return p, nil
		}
	}
	return p, errors.New("no unused private HTTPS port; preserve existing routes")
}

// BindOrigin preserves the recorded isolated port while requiring the same
// Tailscale device reported by the daemon.
func BindOrigin(p Plan, origin string) (Plan, error) {
	base, _, err := tailnet.Origin(p.Origin)
	if err != nil {
		return p, err
	}
	host, _, err := tailnet.Origin(origin)
	if err != nil || host != base {
		return p, errors.New("Tailscale device differs from the installed private origin")
	}
	old := p.Origin
	p.Origin = origin
	for i := range p.Instructions {
		p.Instructions[i] = strings.ReplaceAll(p.Instructions[i], old, origin)
		if origin != old {
			p.Instructions[i] = strings.ReplaceAll(p.Instructions[i], "an unused port 443", "the recorded private HTTPS port")
		}
	}
	return p, nil
}

func inventory(cfg map[string]any) (map[string]any, map[string]any, error) {
	for key := range cfg {
		if key != "TCP" && key != "Web" {
			return nil, nil, errors.New("unsupported Serve/Funnel policy; preserve it")
		}
	}
	read := func(key string) (map[string]any, error) {
		value, found := cfg[key]
		if !found {
			return map[string]any{}, nil
		}
		m, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("unexpected Serve inventory")
		}
		return m, nil
	}
	tcp, err := read("TCP")
	if err != nil {
		return nil, nil, err
	}
	web, err := read("Web")
	if err != nil {
		return nil, nil, err
	}
	validPort := func(value string) bool {
		n, e := strconv.Atoi(value)
		return e == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == value
	}
	for port := range tcp {
		if !validPort(port) {
			return nil, nil, errors.New("unexpected Serve port identity")
		}
	}
	for name := range web {
		host, port, e := net.SplitHostPort(name)
		if e != nil || host == "" || !validPort(port) {
			return nil, nil, errors.New("unexpected Serve host identity")
		}
	}
	return tcp, web, nil
}

func routeState(cfg map[string]any, p Plan) (string, error) {
	hostport, port, err := routeParts(p)
	if err != nil {
		return "", err
	}
	tcp, web, err := inventory(cfg)
	if err != nil {
		return "", err
	}
	for host := range web {
		if strings.HasSuffix(host, ":"+port) && host != hostport {
			return "", errors.New("private Serve port is already used")
		}
	}
	t, hasTCP := tcp[port]
	w, hasWeb := web[hostport]
	if !hasTCP && !hasWeb {
		return "absent", nil
	}
	expect := expected(p)
	if hasTCP && hasWeb && reflect.DeepEqual(t, expect["TCP"].(map[string]any)[port]) && reflect.DeepEqual(w, expect["Web"].(map[string]any)[hostport]) {
		return "owned", nil
	}
	return "", errors.New("private Serve port or route belongs to another configuration")
}

func routeChange(cfg map[string]any, p Plan, add bool) (map[string]any, error) {
	state, err := routeState(cfg, p)
	if err != nil {
		return nil, err
	}
	if add && state != "absent" || !add && state != "owned" {
		return nil, errors.New("private route state changed; preserve it")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var next map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&next) != nil {
		return nil, errors.New("invalid Serve inventory")
	}
	hostport, port, _ := routeParts(p)
	tcp, web, _ := inventory(next)
	if add {
		tcp[port] = map[string]any{"HTTPS": true}
		web[hostport] = map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": p.Target}}}
		next["TCP"], next["Web"] = tcp, web
	} else {
		delete(tcp, port)
		delete(web, hostport)
		if len(tcp) == 0 {
			delete(next, "TCP")
		} else {
			next["TCP"] = tcp
		}
		if len(web) == 0 {
			delete(next, "Web")
		} else {
			next["Web"] = web
		}
	}
	return next, nil
}

// routeDeltaAllowed proves that the complete new inventory differs from the
// old one only by one receipt-owned route or its recorded in-place handoff.
func routeDeltaAllowed(before, after map[string]any, plans []Plan, owns func(Plan) bool) bool {
	for _, p := range plans {
		if !owns(p) {
			continue
		}
		oldState, oldErr := routeState(before, p)
		newState, newErr := routeState(after, p)
		if oldErr == nil && newErr == nil && oldState != newState {
			next, err := routeChange(before, p, oldState == "absent")
			if err == nil && reflect.DeepEqual(next, after) {
				return true
			}
		}
		if oldErr != nil || oldState != "owned" {
			continue
		}
		for _, q := range plans {
			if q.Origin != p.Origin || q.Target == p.Target || !owns(q) {
				continue
			}
			state, err := routeState(after, q)
			if err != nil || state != "owned" {
				continue
			}
			removed, err := routeChange(before, p, false)
			if err != nil {
				continue
			}
			next, err := routeChange(removed, q, true)
			if err == nil && reflect.DeepEqual(next, after) {
				return true
			}
		}
	}
	return false
}
