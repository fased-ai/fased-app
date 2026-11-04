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
	key := strings.TrimSpace(suggested)
	if key != "" {
		fmt.Fprintln(out, "Use the SSH public key from this bootstrap connection for private recovery? [Y/n]:")
		answer, e := in.ReadString('\n')
		if e != nil {
			return e
		}
		if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "" && answer != "y" && answer != "yes" {
			return errors.New("Remote setup cancelled; a verified recovery key is required")
		}
	} else {
		fmt.Fprintln(out, "Paste your SSH public key for private recovery (never a private key):")
		value, e := in.ReadString('\n')
		if e != nil {
			return e
		}
		key = strings.TrimSpace(value)
	}
	if !validRecoveryPublicKey(key) {
		return errors.New("use a single SSH public key without options or private key material")
	}
	temp, err := os.CreateTemp(home, ".fased-public-key-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err = temp.WriteString(key + "\n"); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	// ssh-keygen is an ordinary OS tool; the key input is data, never shell text.
	command := exec.Command("/usr/bin/ssh-keygen", "-lf", temp.Name())
	fingerprint, err := command.Output()
	if err != nil || len(fingerprint) > 4096 {
		return errors.New("SSH rejected this public key")
	}
	if strings.HasPrefix(key, "ssh-rsa ") {
		fields := strings.Fields(string(fingerprint))
		if len(fields) == 0 {
			return errors.New("invalid recovery key")
		}
		bits, e := strconv.Atoi(fields[0])
		if e != nil || bits < 3072 {
			return errors.New("use an Ed25519 key or an RSA key of at least 3072 bits")
		}
	}
	fmt.Fprintf(out, "Recovery key: %s", fingerprint)
	if err = core.EnsurePrivateDirectory(dir); err != nil {
		return err
	}
	// Publish only a complete verified key, without replacing any existing file.
	if err = os.Link(temp.Name(), path); err != nil {
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
