// Package privateaccess owns only a previously empty Tailscale Serve endpoint.
// It never installs Tailscale, enrolls a device, enables Funnel or changes SSH.
package privateaccess

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
)

type Runner func(context.Context, ...string) ([]byte, error)
type Manager struct {
	Run          Runner
	GetConfig    func(context.Context) ([]byte, string, error)
	SetConfig    func(context.Context, []byte, string) error
	Client       *http.Client
	Proof        func(context.Context, core.PrivateAccessProofRequest) (core.PrivateAccessProof, error)
	ProofOut     io.Writer
	ExpectedPeer *tailnet.Identity
	InitialPeer  func() (tailnet.Identity, bool, error)
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
	Next   *Plan  `json:"next,omitempty"`
	Plan   Plan   `json:"plan"`
}

// A route change must not outlive the record needed to reconcile it after a
// crash. WritePrivateJSON syncs the file; the containing directory must also
// be synced after its rename (and after a completed removal).
func syncAccessDirectory(root string) error {
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func storeAccessReceipt(root string, r receipt) error {
	if err := core.WritePrivateJSON(filepath.Join(root, "private-access.json"), r); err != nil {
		return err
	}
	return syncAccessDirectory(root)
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
	c, err := decodeServeInventory(data, true)
	if err != nil {
		return nil, "", err
	}
	return c, etag, nil
}

// Tailscale represents an absent current Serve configuration as JSON null.
// Only reads of the current daemon state may normalize it to an empty object;
// requested writes must remain explicit objects and retain their ETag.
func decodeServeInventory(data []byte, allowNull bool) (map[string]any, error) {
	var c map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF || c == nil && !allowNull {
		return nil, errors.New("invalid Serve configuration; refusing mutation")
	}
	if c == nil {
		c = make(map[string]any)
	}
	// Some versions encode an unused optional section as null or an empty map.
	for k, v := range c {
		if v == nil {
			delete(c, k)
		} else if x, ok := v.(map[string]any); ok && len(x) == 0 {
			delete(c, k)
		}
	}
	return c, nil
}
func expected(p Plan) map[string]any {
	host, port, _ := tailnet.Origin(p.Origin)
	return map[string]any{"TCP": map[string]any{port: map[string]any{"HTTPS": true}}, "Web": map[string]any{net.JoinHostPort(host, port): map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": p.Target}}}}}
}
func load(root string) (receipt, error) {
	data, err := core.ReadPrivateFile(filepath.Join(root, "private-access.json"))
	if err != nil {
		return receipt{}, err
	}
	return parseReceipt(data)
}

func parseReceipt(data []byte) (receipt, error) {
	var r receipt
	if json.Unmarshal(data, &r) != nil || r.Schema != 1 || r.Plan.Mode != "tailscale" || (r.Phase != "preparing" && r.Phase != "ready" && r.Phase != "switching") {
		return r, errors.New("unknown private-access receipt; preserve it for recovery")
	}
	if _, _, err := tailnet.Origin(r.Plan.Origin); err != nil {
		return r, errors.New("unknown private-access origin; preserve it")
	}
	if r.Phase == "switching" {
		if r.Next == nil || r.Next.Mode != "tailscale" || r.Next.Origin != r.Plan.Origin || !validTarget(r.Next.Target) || !strings.HasPrefix(r.Next.Target, "unix:") {
			return r, errors.New("invalid private ingress transition; preserve it")
		}
	} else if r.Next != nil {
		return r, errors.New("unexpected private ingress transition; preserve it")
	}
	if !validTarget(r.Plan.Target) {
		return r, errors.New("invalid private target")
	}
	return r, nil
}
func (m Manager) Enable(ctx context.Context, root string, p Plan, instanceID string) error {
	if p.Mode != "tailscale" {
		return nil
	}
	if _, _, err := routeParts(p); err != nil {
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
		state, e := routeState(cfg, p)
		if e != nil || state != "absent" {
			return errors.New("existing private route belongs to the owner; refusing to replace or adopt it")
		}
		r = receipt{Schema: 1, Phase: "preparing", Plan: p}
		if err = storeAccessReceipt(root, r); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if r.Plan.Origin == p.Origin && r.Plan.Target != p.Target && strings.HasPrefix(p.Target, "unix:") && (r.Phase == "ready" || r.Phase == "switching") {
		return m.switchIngress(ctx, root, r, p, instanceID, cfg, etag)
	} else if r.Plan.Origin != p.Origin || r.Plan.Target != p.Target {
		return errors.New("private-access identity changed; recover the existing route first")
	}
	state, err := routeState(cfg, p)
	if err != nil {
		return err
	}
	if state == "absent" {
		// A prior process may have published the record before a power cut.
		if err = syncAccessDirectory(root); err != nil {
			return err
		}
		changed, e := routeChange(cfg, p, true)
		if e != nil {
			return e
		}
		next, _ := json.Marshal(changed)
		if err = m.SetConfig(ctx, next, etag); err != nil {
			return err
		}
		cfg, _, err = m.config(ctx)
		if err != nil {
			return err
		}
	}
	if state, err = routeState(cfg, p); err != nil || state != "owned" {
		return errors.New("Serve/Funnel configuration differs; no reset or overwrite was attempted")
	}
	if r.Phase == "preparing" {
		err = m.waitForPreparingEndpoint(ctx, p, instanceID)
	} else {
		err = m.Verify(ctx, p, instanceID)
	}
	if err != nil {
		return err
	}
	r.Phase = "ready"
	return storeAccessReceipt(root, r)
}

var errEndpointStarting = errors.New("private HTTPS is starting; retain SSH/console access")
var errEndpointSelf = errors.New("verify private HTTPS from another approved Tailscale device; retain SSH/console access")

// Status identifies a self-address; it never proves that another device can
// reach Serve. Those checks must use an approved external device proof.
func (m Manager) ownTailscaleOrigin(ctx context.Context, origin string) (bool, string, error) {
	if m.Run == nil {
		return false, "", nil
	}
	data, err := m.Run(ctx, "status", "--json")
	if err != nil || len(data) > 1024*1024 {
		return false, "", errors.New("cannot verify the local Tailscale device; preserve private access")
	}
	var status struct {
		BackendState string
		Self         struct {
			ID      string
			DNSName string
			Online  bool
		}
	}
	if json.Unmarshal(data, &status) != nil || status.BackendState != "Running" || !status.Self.Online {
		return false, "", errors.New("the local Tailscale device is not ready; preserve private access")
	}
	name := strings.TrimSuffix(status.Self.DNSName, ".")
	host, _, err := tailnet.Origin(origin)
	if err != nil || !dnsName.MatchString(name) {
		return false, "", errors.New("invalid Tailscale device identity")
	}
	if host == name && (status.Self.ID == "" || len(status.Self.ID) > 128) {
		return false, "", errors.New("the serving Tailscale device identity is unavailable")
	}
	return host == name, status.Self.ID, nil
}

// A new Serve endpoint can take time to obtain its first certificate. Keep each
// probe bounded, retry only transport startup, and never rewrite the route.
func (m Manager) waitForPreparingEndpoint(ctx context.Context, p Plan, instanceID string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := m.Verify(ctx, p, instanceID)
		if !errors.Is(err, errEndpointStarting) {
			return err
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Verify checks the existing private route without changing Serve or owner state.
// The exact inventory excludes Funnel and unrelated/foreign endpoints.
func (m Manager) Verify(ctx context.Context, p Plan, instanceID string) error {
	if _, _, err := routeParts(p); err != nil {
		return errors.New("private Tailscale HTTPS route required")
	}
	cfg, _, err := m.config(ctx)
	if err != nil {
		return err
	}
	if state, err := routeState(cfg, p); err != nil || state != "owned" {
		return errors.New("Serve/Funnel configuration changed; preserve access and inspect it before lockdown")
	}
	if err := m.verifyEndpoint(ctx, p, instanceID); err != nil {
		return err
	}
	// The proof takes place on another device, so recheck the owned route and
	// Funnel boundary before using it for readiness or network lockdown.
	cfg, _, err = m.config(ctx)
	if err != nil {
		return err
	}
	if state, err := routeState(cfg, p); err != nil || state != "owned" {
		return errors.New("Serve/Funnel configuration changed during private verification")
	}
	return nil
}

func (m Manager) verifyEndpoint(ctx context.Context, p Plan, instanceID string) error {
	self, selfID, err := m.ownTailscaleOrigin(ctx, p.Origin)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if self {
		if m.Proof == nil || m.ProofOut == nil {
			return errEndpointSelf
		}
		return m.verifyFromDevice(ctx, p, instanceID, selfID)
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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var networkError net.Error
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &networkError) && networkError.Timeout()) {
			return errEndpointStarting
		}
		return errors.New("private HTTPS could not be verified; retain SSH/console access and retry this command")
	}
	defer resp.Body.Close()
	if _, err = connection.ReadHealth(resp, instanceID); err != nil {
		return errors.New("private HTTPS reached a different or incompatible app; route remains recorded for recovery")
	}
	return nil
}

// verifyFromDevice asks the existing private ingress to observe a one-use
// challenge. The status reader is bound to app-owner IPC, and the core binds
// confirmation to the Tailscale device that actually reached this instance.
func (m Manager) verifyFromDevice(ctx context.Context, p Plan, instanceID, selfID string) error {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	request := core.PrivateAccessProofRequest{Action: "begin", Token: token}
	proof, err := m.Proof(ctx, request)
	if err != nil || proof.State != "pending" || proof.InstanceID != instanceID {
		return errors.New("private access proof could not be started for this instance")
	}
	if _, err = fmt.Fprintf(m.ProofOut, "Open this private Fased link on your approved Tailscale device:\n%s/.fased-private-access#%s\n", p.Origin, token); err != nil {
		return err
	}
	request.Action = "wait"
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		proof, err = m.Proof(ctx, request)
		if err != nil {
			return fmt.Errorf("private device confirmation did not complete: %w", err)
		}
		if proof.InstanceID != instanceID {
			return errors.New("private confirmation reached a different instance")
		}
		if proof.State == "pending" {
			// The normal IPC wait blocks for up to six seconds. A fast pending
			// response must not create an unbounded helper-launch loop.
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if proof.State != "verified" || proof.UserID <= 0 || proof.NodeID == "" || proof.NodeID == selfID {
			return errors.New("invalid private device confirmation")
		}
		if m.ExpectedPeer != nil && (proof.UserID != m.ExpectedPeer.UserID || proof.NodeID != m.ExpectedPeer.NodeID) {
			return errors.New("private confirmation came from a different device")
		}
		if !proof.OwnerBound {
			if m.InitialPeer == nil {
				return errors.New("initial Remote device handoff is required before verification")
			}
			initial, found, e := m.InitialPeer()
			if e != nil || !found || proof.UserID != initial.UserID || proof.NodeID != initial.NodeID {
				return errors.New("private confirmation differs from the initial device handoff")
			}
		}
		return nil
	}
}
func (m Manager) Remove(ctx context.Context, root string) error {
	lock, err := core.LockInstance(filepath.Join(root, "access.lock"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	r, err := load(root)
	if errors.Is(err, os.ErrNotExist) {
		// Managed Tailscale Serve is Linux-only. Windows directory handles
		// cannot be synced, and no managed route receipt exists to recover.
		if runtime.GOOS == "windows" {
			return nil
		}
		// A preceding removal may have unlinked the receipt but failed to
		// sync the directory. Retrying must finish that durability barrier.
		return syncAccessDirectory(root)
	}
	if err != nil {
		return err
	}
	if r.Phase == "switching" {
		return errors.New("private ingress transition is unfinished; resume the recorded route before removal")
	}
	cfg, etag, err := m.config(ctx)
	if err != nil {
		return err
	}
	state, err := routeState(cfg, r.Plan)
	if err != nil {
		return errors.New("private route changed; refusing removal")
	}
	if state == "owned" {
		next, e := routeChange(cfg, r.Plan, false)
		if e != nil {
			return e
		}
		data, _ := json.Marshal(next)
		if err = m.SetConfig(ctx, data, etag); err != nil {
			return err
		}
		cfg, _, err = m.config(ctx)
		state, e = routeState(cfg, r.Plan)
		if err != nil || e != nil || state != "absent" {
			return errors.New("route removal is not confirmed; retry after inspection")
		}
	}
	if err = os.Remove(filepath.Join(root, "private-access.json")); err != nil {
		return err
	}
	return syncAccessDirectory(root)
}

// switchIngress resumes the exact owned transition to protected Unix ingress. A lost response
// does not reset Serve, discard ownership or reopen access. Once device binding
// may have happened, recover forward instead of silently restoring legacy HTTP.
func (m Manager) switchIngress(ctx context.Context, root string, r receipt, next Plan, instance string, cfg map[string]any, etag string) error {
	if r.Next != nil && (r.Next.Mode != next.Mode || r.Next.Origin != next.Origin || r.Next.Target != next.Target) {
		return errors.New("another private ingress transition owns this receipt")
	}
	oldState, _ := routeState(cfg, r.Plan)
	newState, _ := routeState(cfg, next)
	if oldState != "owned" && newState != "owned" {
		return errors.New("private route changed; preserve its transition receipt")
	}
	if r.Phase != "switching" {
		r.Phase = "switching"
		r.Next = &next
		if err := storeAccessReceipt(root, r); err != nil {
			return err
		}
	}
	// The retained switching receipt might have been written by a predecessor
	// that did not sync the directory. Protect both the pending write and the
	// lost-response path, where the next route already appears in Serve.
	if err := syncAccessDirectory(root); err != nil {
		return err
	}
	if oldState == "owned" {
		removed, err := routeChange(cfg, r.Plan, false)
		if err != nil {
			return err
		}
		changed, err := routeChange(removed, next, true)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(changed)
		if err := m.SetConfig(ctx, data, etag); err != nil {
			return err
		}
	}
	if err := m.waitForPreparingEndpoint(ctx, next, instance); err != nil {
		return err
	}
	current, _, err := m.config(ctx)
	if err != nil {
		return err
	}
	if state, err := routeState(current, next); err != nil || state != "owned" {
		return errors.New("private route changed before transition completion")
	}
	return storeAccessReceipt(root, receipt{Schema: 1, Phase: "ready", Plan: next})
}
