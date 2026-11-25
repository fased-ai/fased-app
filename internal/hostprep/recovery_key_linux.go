//go:build linux

package hostprep

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
)

const maxRecoveryKeyCandidates = 16

// PrepareRecoveryKey prepares only the ordinary human operator's SSH key login.
// It grants no app/signer privileges, never copies a key list, creates no password
// and never closes access. A fresh private SSH login must still be proven.
func PrepareRecoveryKey(in *bufio.Reader, out io.Writer) error {
	owner, err := user.Current()
	if err != nil {
		return err
	}
	if owner.Uid != fmt.Sprint(os.Getuid()) || os.Getuid() <= 0 {
		return errors.New("ordinary recovery administrator required")
	}
	return prepareRecoveryKey(owner.HomeDir, in, out, os.Getenv("FASED_BOOTSTRAP_RECOVERY_KEY"))
}

func parseRecoveryCandidates(suggested string) []string {
	seen := map[string]bool{}
	var candidates []string
	for _, raw := range strings.Split(suggested, "\n") {
		k := strings.TrimSpace(raw)
		if k == "" || !validRecoveryPublicKey(k) || seen[k] {
			continue
		}
		seen[k] = true
		candidates = append(candidates, k)
		if len(candidates) >= maxRecoveryKeyCandidates {
			break
		}
	}
	return candidates
}

func stageAndVerifyRecoveryKey(home, key string) (tempPath, fingerprint string, cleanup func(), err error) {
	if !validRecoveryPublicKey(key) {
		return "", "", nil, errors.New("use a single SSH public key without options or private key material")
	}
	temp, err := os.CreateTemp(home, ".fased-public-key-")
	if err != nil {
		return "", "", nil, err
	}
	removeTemp := func() { _ = os.Remove(temp.Name()) }
	if _, err = temp.WriteString(key + "\n"); err != nil {
		_ = temp.Close()
		removeTemp()
		return "", "", nil, err
	}
	if err = temp.Sync(); err != nil {
		_ = temp.Close()
		removeTemp()
		return "", "", nil, err
	}
	if err = temp.Close(); err != nil {
		removeTemp()
		return "", "", nil, err
	}
	// ssh-keygen is an ordinary OS tool; the key input is data, never shell text.
	command := exec.Command("/usr/bin/ssh-keygen", "-lf", temp.Name())
	fpBytes, err := command.Output()
	if err != nil || len(fpBytes) > 4096 {
		removeTemp()
		return "", "", nil, errors.New("SSH rejected this public key")
	}
	if strings.HasPrefix(key, "ssh-rsa ") {
		fields := strings.Fields(string(fpBytes))
		if len(fields) == 0 {
			removeTemp()
			return "", "", nil, errors.New("invalid recovery key")
		}
		bits, e := strconv.Atoi(fields[0])
		if e != nil || bits < 3072 {
			removeTemp()
			return "", "", nil, errors.New("use an Ed25519 key or an RSA key of at least 3072 bits")
		}
	}
	return temp.Name(), string(fpBytes), removeTemp, nil
}

func prepareRecoveryKey(home string, in *bufio.Reader, out io.Writer, suggested string) error {
	dir := filepath.Join(home, ".ssh")
	path := filepath.Join(dir, "authorized_keys")
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode().Perm() != 0700 {
			return errors.New("preserve and review existing SSH directory permissions before setup")
		}
		if data, e := core.ReadPrivateFile(path); e == nil {
			if len(bytes.TrimSpace(data)) == 0 {
				return errors.New("existing empty SSH key file needs review")
			}
			return nil
		} else if !os.IsNotExist(e) {
			return e
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if in == nil {
		return errors.New("a recovery SSH public key is required before Remote deployment")
	}
	candidates := parseRecoveryCandidates(suggested)
	var key string
	switch len(candidates) {
	case 0:
		fmt.Fprintln(out, "Paste your SSH public key for private recovery (never a private key):")
		value, err := in.ReadString('\n')
		if err != nil {
			return err
		}
		key = strings.TrimSpace(value)
	case 1:
		fmt.Fprintln(out, "Use the SSH public key from this bootstrap connection for private recovery? [Y/n]:")
		answer, err := in.ReadString('\n')
		if err != nil {
			return err
		}
		if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "" && answer != "y" && answer != "yes" {
			return errors.New("Remote setup cancelled; a verified recovery key is required")
		}
		key = candidates[0]
	default:
		type verifiedCandidate struct {
			key, fp string
		}
		var verified []verifiedCandidate
		for _, c := range candidates {
			_, fp, cleanup, err := stageAndVerifyRecoveryKey(home, c)
			if cleanup != nil {
				cleanup()
			}
			if err != nil {
				continue
			}
			verified = append(verified, verifiedCandidate{key: c, fp: strings.TrimSpace(fp)})
		}
		if len(verified) == 0 {
			return errors.New("no valid recovery SSH public key found in bootstrap candidates")
		}
		fmt.Fprintln(out, "Multiple SSH public keys found in bootstrap authorized_keys:")
		for i, c := range verified {
			fmt.Fprintf(out, "  %d) %s\n", i+1, c.fp)
		}
		fmt.Fprintf(out, "Select one SSH public key for private recovery [1-%d, default 1], or paste a key:\n", len(verified))
		answer, err := in.ReadString('\n')
		if err != nil {
			return err
		}
		answer = strings.TrimSpace(answer)
		switch {
		case answer == "":
			key = verified[0].key
		default:
			if idx, idxErr := strconv.Atoi(answer); idxErr == nil {
				if idx < 1 || idx > len(verified) {
					return errors.New("invalid recovery SSH key selection")
				}
				key = verified[idx-1].key
			} else {
				if !validRecoveryPublicKey(answer) {
					return errors.New("invalid recovery SSH key selection")
				}
				key = answer
			}
		}
	}
	tempPath, fingerprint, cleanup, err := stageAndVerifyRecoveryKey(home, key)
	if err != nil {
		return err
	}
	defer cleanup()
	fmt.Fprintf(out, "Recovery key: %s", fingerprint)
	if err = core.EnsurePrivateDirectory(dir); err != nil {
		return err
	}
	// Publish only a complete verified single key, without replacing any existing file.
	if err = os.Link(tempPath, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validRecoveryPublicKey(value string) bool {
	if len(value) > 16384 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		return true
	}
	return false
}
