// Package ownerclient is a short-lived OS-owner input client. It is never linked
// into an HTTP owner route and never authenticates with a core/browser session.
package ownerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/signer"
)

type Peer struct {
	Ready        *bool  `json:"ready,omitempty"`
	Version      int    `json:"version"`
	InstanceID   string `json:"instanceId"`
	SignerID     string `json:"signerId"`
	Network      string `json:"network"`
	OwnerUID     int    `json:"ownerUid"`
	CoreSHA256   string `json:"coreSha256,omitempty"`
	SignerSHA256 string `json:"signerSha256,omitempty"`
}

type command func(context.Context, string, []string, []byte) ([]byte, error)
type Client struct {
	Intent                 signer.OwnerIntent
	Peer                   Peer
	host, user, knownHosts string
	run                    command
}

var sshUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
var ErrEnrollmentRequired = errors.New("protected wallet enrollment is required")

// Open never receives a secret. Remote uses only a native-user-selected account,
// system SSH keys/agent and an ALREADY verified known_hosts pin; no TOFU prompt,
// password fallback, PTY, forwarding, shell fragment or core-supplied command.
func Open(ctx context.Context, intent signer.OwnerIntent, origin, user string) (*Client, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return open(ctx, intent, origin, user, filepath.Join(home, ".ssh", "known_hosts"), os.Getuid(), run)
}

func open(ctx context.Context, intent signer.OwnerIntent, origin, user, pins string, uid int, execute command) (*Client, error) {
	c, err := locate(ctx, intent, origin, user, pins, uid, execute)
	if err != nil {
		return nil, err
	}
	if err = c.inspect(ctx); err != nil {
		if errors.Is(err, ErrEnrollmentRequired) {
			return nil, err
		}
		_, missing := c.invoke(ctx, "/usr/bin/test", []string{"-e", "/var/lib/fased-app-custody-" + strconv.Itoa(c.Peer.OwnerUID) + "/install.json"}, nil)
		var exit *commandExit
		if errors.As(missing, &exit) && exit.code == 1 {
			return nil, ErrEnrollmentRequired
		}
		return nil, err
	}
	if intent.Version == 2 {
		p, err := intent.TradeProposal()
		if err != nil || p.Account.SignerID != c.Peer.SignerID {
			return nil, errors.New("trade belongs to another enrolled signer")
		}
	}
	return c, nil
}

func locate(ctx context.Context, intent signer.OwnerIntent, origin, user, pins string, uid int, execute command) (*Client, error) {
	if intent.Validate() != nil || uid <= 0 {
		return nil, errors.New("wallet owner requires an ordinary OS account")
	}
	c := &Client{Intent: intent, knownHosts: pins, run: execute}
	if origin != "" {
		canonical, err := connection.Origin(origin)
		u, parseErr := url.Parse(canonical)
		if err != nil || parseErr != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".ts.net") || u.Port() != "" || !sshUser.MatchString(user) {
			return nil, errors.New("choose the private Tailscale instance and its non-root OS owner")
		}
		c.host, c.user = u.Hostname(), user
		b, err := c.invoke(ctx, "/usr/bin/id", []string{"-u"}, nil)
		if err != nil {
			return nil, errors.New("private owner connection requires an existing verified SSH host key and key-only access")
		}
		uid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || uid <= 0 {
			return nil, errors.New("remote wallet owner must be non-root")
		}
	}
	c.Peer = Peer{Version: 1, InstanceID: intent.InstanceID, Network: intent.Network, OwnerUID: uid}
	return c, nil
}

func (c *Client) inspect(ctx context.Context) error {
	uid := c.Peer.OwnerUID
	root := "/var/lib/fased-app-custody-" + strconv.Itoa(uid)
	b, err := c.invoke(ctx, root+"/fased-app", append(c.arguments(), "--inspect"), nil)
	if err != nil {
		return errors.New("protected wallet enrollment is required for this instance and network")
	}
	var peer Peer
	if decode(b, &peer) != nil || peer.Version != 1 || peer.InstanceID != c.Intent.InstanceID || peer.Network != c.Intent.Network || peer.OwnerUID != uid || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(peer.SignerID) {
		return errors.New("owner connection returned a different wallet identity")
	}
	c.Peer = peer
	if peer.Ready != nil && !*peer.Ready {
		return ErrEnrollmentRequired
	}
	return nil
}

func (c *Client) arguments() []string {
	return []string{"custody", "window", "--expected-instance", c.Intent.InstanceID, "--network", c.Intent.Network, "--disposable-fixture"}
}

func (c *Client) invoke(ctx context.Context, path string, args []string, input []byte) ([]byte, error) {
	if c.host == "" {
		return c.run(ctx, path, args, input)
	}
	ssh := append([]string{"-F", "/dev/null", "-T", "-a"}, c.sshPolicy()...)
	ssh = append(ssh, "-l", c.user, "--", c.host, path)
	// Remote SSH concatenates arguments into a shell command. Every token is
	// generated here, or validated as canonical base64url/network/decimal input.
	ssh = append(ssh, args...)
	return c.run(ctx, "/usr/bin/ssh", ssh, input)
}

func (c *Client) sshPolicy() []string {
	return []string{"-oBatchMode=yes", "-oStrictHostKeyChecking=yes", "-oUserKnownHostsFile=" + c.knownHosts, "-oGlobalKnownHostsFile=/dev/null", "-oPasswordAuthentication=no", "-oKbdInteractiveAuthentication=no", "-oClearAllForwardings=yes", "-oPermitLocalCommand=no", "-oProxyCommand=none", "-oControlMaster=no", "-oControlPath=none", "-oConnectTimeout=10", "-oConnectionAttempts=1"}
}

type commandExit struct{ code int }

func (e *commandExit) Error() string { return "protected owner command unavailable" }

func (c *Client) Exchange(ctx context.Context, input signer.OwnerInput) (signer.OwnerResult, error) {
	var result signer.OwnerResult
	if input.Intent != c.Intent || input.SignerID != c.Peer.SignerID || input.Validate() != nil {
		return result, errors.New("wallet owner input changed after target verification")
	}
	b, err := json.Marshal(input)
	if err != nil {
		return result, errors.New("invalid protected input")
	}
	defer clear(b)
	response, err := c.invoke(ctx, "/var/lib/fased-app-custody-"+strconv.Itoa(c.Peer.OwnerUID)+"/fased-app", c.arguments(), b)
	defer clear(response)
	if err != nil {
		return result, errors.New("wallet owner operation interrupted; resume this operation")
	}
	if input.Intent.Version == 2 {
		return c.validateTradeResponse(response, input)
	}
	if decode(response, &result) != nil || result.Swap != nil || result.ReadOnly || result.Intent != c.Intent || result.SignerID != c.Peer.SignerID || result.Status.Account == nil || result.Status.Account.SignerID != c.Peer.SignerID || result.Status.Account.Network != c.Intent.Network || !result.Status.Locked || !result.Status.RecoveryRequired || result.Status.WalletSigner || result.Status.FinancialExecution || len(result.Backup) == 0 || result.Fingerprint != signer.BackupFingerprint(result.Backup) || result.BackupVerified != (input.Phase == "verify") {
		return signer.OwnerResult{}, errors.New("wallet owner returned an invalid completion")
	}
	if !signer.ValidManagedAccountRef(*result.Status.Account) && !signer.ValidOrdinaryAccountRef(*result.Status.Account) {
		return signer.OwnerResult{}, errors.New("wallet owner returned an invalid account")
	}
	return result, nil
}

func decode(b []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing owner data")
	}
	return nil
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > signer.MaxOwnerInput {
		return 0, errors.New("oversized owner response")
	}
	return b.Buffer.Write(p)
}

func run(ctx context.Context, path string, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = bytes.NewReader(input)
	var out boundedOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if err := cmd.Run(); err != nil {
		clear(out.Bytes())
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, &commandExit{exit.ExitCode()}
		}
		return nil, errors.New("protected owner helper unavailable")
	}
	return out.Bytes(), nil
}
