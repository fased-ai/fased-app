package ownerclient

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/connection"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
)

// Link carries public intent only. It never selects an executable, SSH account,
// host key, filesystem path, signer RPC action, seed or credential.
func Link(instance, network, action, origin string) (string, error) {
	intent := signer.OwnerIntent{Version: 1, InstanceID: instance, Network: network, Action: action, OperationID: "00000000000000000000000000000000"}
	if intent.Validate() != nil {
		return "", errors.New("invalid wallet intent")
	}
	q := url.Values{"instance": {instance}, "network": {network}, "action": {action}}
	if origin != "" {
		canonical, err := connection.Origin(origin)
		if err != nil {
			return "", err
		}
		u, err := url.Parse(canonical)
		if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".ts.net") || u.Port() != "" {
			return "", errors.New("wallet link requires the private Tailscale origin")
		}
		q.Set("origin", canonical)
	}
	return "fased://wallet-owner?" + q.Encode(), nil
}

func ParseLink(value string) (signer.OwnerIntent, string, error) {
	var intent signer.OwnerIntent
	u, err := url.Parse(value)
	if err != nil || len(value) > 4096 || u.Scheme != "fased" || u.Host != "wallet-owner" || u.Path != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return intent, "", errors.New("invalid wallet link")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return intent, "", errors.New("invalid wallet link")
	}
	for key, values := range q {
		if len(values) != 1 || (key != "instance" && key != "network" && key != "action" && key != "origin") {
			return intent, "", errors.New("invalid wallet link field")
		}
	}
	canonical, err := Link(q.Get("instance"), q.Get("network"), q.Get("action"), q.Get("origin"))
	if err != nil || canonical != value {
		return intent, "", errors.New("noncanonical wallet link")
	}
	intent = signer.OwnerIntent{Version: 1, InstanceID: q.Get("instance"), Network: q.Get("network"), Action: q.Get("action"), OperationID: "00000000000000000000000000000000"}
	return intent, q.Get("origin"), nil
}

// Resume uses one public owner draft for the same instance/action/network. No
// request or secret is persisted. A relaunch cannot silently create another key.
type draft struct {
	Intent     signer.OwnerIntent `json:"intent"`
	Origin     string             `json:"origin"`
	Dispatched bool               `json:"dispatched"`
}

func Resume(root, link string) (signer.OwnerIntent, string, error) {
	intent, origin, err := ParseLink(link)
	if err != nil {
		return intent, origin, err
	}
	if err = protectRoot(root); err != nil {
		return intent, origin, err
	}
	lock, err := core.LockInstance(filepath.Join(root, "owner-client.lock"))
	if err != nil {
		return intent, origin, err
	}
	defer lock.Close()
	path := filepath.Join(root, intent.InstanceID+".operation.json")
	b, err := core.ReadPrivateFile(path)
	if err == nil {
		var saved draft
		if decode(b, &saved) != nil || saved.Intent.Validate() != nil || saved.Intent.InstanceID != intent.InstanceID {
			return intent, origin, errors.New("invalid saved owner operation")
		}
		if saved.Intent.Action == intent.Action && saved.Intent.Network == intent.Network && saved.Origin == origin {
			return saved.Intent, origin, nil
		}
		if saved.Dispatched {
			return intent, origin, errors.New("resume the saved wallet operation for this instance")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return intent, origin, err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return intent, origin, err
	}
	intent.OperationID = hex.EncodeToString(id[:])
	err = writeState(path, draft{Intent: intent, Origin: origin})
	return intent, origin, err
}

// Persist possible dispatch before the first private write. Ambiguous failures
// retain the original operation; closing an unsubmitted window remains harmless.
func MarkDispatched(root string, intent signer.OwnerIntent) error {
	if err := protectRoot(root); err != nil {
		return err
	}
	lock, err := core.LockInstance(filepath.Join(root, "owner-client.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	path := filepath.Join(root, intent.InstanceID+".operation.json")
	b, err := core.ReadPrivateFile(path)
	if err != nil {
		return err
	}
	var saved draft
	if decode(b, &saved) != nil || saved.Intent != intent {
		return errors.New("wallet draft changed before dispatch")
	}
	saved.Dispatched = true
	return writeState(path, saved)
}

type ownerTarget struct {
	Origin string `json:"origin"`
	User   string `json:"user"`
	Peer   Peer   `json:"peer"`
}

func SavedTarget(root, instance string) (string, string, error) {
	if root == "" || len(instance) != 43 {
		return "", "", errors.New("no saved owner target")
	}
	if err := protectRoot(root); err != nil {
		return "", "", err
	}
	b, err := core.ReadPrivateFile(filepath.Join(root, instance+".target.json"))
	if err != nil {
		return "", "", err
	}
	var saved ownerTarget
	if decode(b, &saved) != nil || saved.Peer.InstanceID != instance {
		return "", "", errors.New("invalid saved owner target")
	}
	return saved.Origin, saved.User, nil
}

// Called only after explicit target selection in the isolated native window.
// A core-supplied URI is public context, never an approved SSH destination.
func RememberTarget(root, origin, user string, peer Peer) error {
	// Runtime readiness changes across stop/start and is not owner identity.
	peer.Ready = nil
	if err := protectRoot(root); err != nil {
		return err
	}
	lock, err := core.LockInstance(filepath.Join(root, "owner-client.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	path := filepath.Join(root, peer.InstanceID+".target.json")
	b, err := core.ReadPrivateFile(path)
	target := ownerTarget{origin, user, peer}
	if err == nil {
		var prior ownerTarget
		if decode(b, &prior) != nil {
			return errors.New("invalid protected owner target")
		}
		hadReadiness := prior.Peer.Ready != nil
		prior.Peer.Ready = nil
		oldPeer, newPeer := prior.Peer, peer
		newPeer.CoreSHA256, newPeer.SignerSHA256 = "", ""
		oldPeer.CoreSHA256, oldPeer.SignerSHA256 = "", ""
		if prior.Origin != origin || prior.User != user || oldPeer != newPeer {
			return errors.New("the protected owner target changed; existing approval is preserved")
		}
		if hadReadiness || prior != target {
			return writeState(path, target)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeState(path, target)
}

type Backup struct {
	Version     int    `json:"version"`
	InstanceID  string `json:"instanceId"`
	Network     string `json:"network"`
	Fingerprint string `json:"fingerprint"`
	Document    []byte `json:"document"`
}

// SaveBackup writes only ciphertext; a lost reply/relaunch reuses exactly the
// original file. Conflicting or foreign files are never overwritten.
func SaveBackup(root string, result signer.OwnerResult) (string, []byte, error) {
	if err := protectRoot(root); err != nil {
		return "", nil, err
	}
	lock, err := core.LockInstance(filepath.Join(root, "owner-client.lock"))
	if err != nil {
		return "", nil, err
	}
	defer lock.Close()
	path := filepath.Join(root, result.Intent.InstanceID+"-"+result.Intent.OperationID+".backup.json")
	if result.Intent.Validate() != nil || len(result.Backup) == 0 || result.Fingerprint != signer.BackupFingerprint(result.Backup) {
		return "", nil, errors.New("invalid encrypted backup")
	}
	b, err := core.ReadPrivateFile(path)
	var existing Backup
	if err == nil {
		if decode(b, &existing) != nil || existing.Version != 1 || existing.InstanceID != result.Intent.InstanceID || existing.Network != result.Intent.Network || existing.Fingerprint != result.Fingerprint || !bytes.Equal(existing.Document, result.Backup) {
			return "", nil, errors.New("existing owner backup is preserved")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		existing = Backup{1, result.Intent.InstanceID, result.Intent.Network, result.Fingerprint, result.Backup}
		if err = writeState(path, existing); err != nil {
			return "", nil, err
		}

	} else {
		return "", nil, err
	}
	b, err = core.ReadPrivateFile(path)
	if err != nil || decode(b, &existing) != nil || existing.Fingerprint != signer.BackupFingerprint(existing.Document) || !bytes.Equal(existing.Document, result.Backup) {
		return "", nil, errors.New("encrypted backup could not be reopened")
	}
	return path, existing.Document, nil
}

// Directory durability is required before dispatch, not just ciphertext writes.
func writeState(path string, value any) error {
	if err := core.WritePrivateJSON(path, value); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
