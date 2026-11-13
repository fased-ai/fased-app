//go:build linux

package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/fased-ai/fased-app/internal/update"
)

func ReadAuthority(owner int) (Authority, error) {
	var a Authority
	if owner <= 0 {
		return a, errors.New("ordinary maintenance owner required")
	}
	path := OwnerRoot(owner) + "/authority.json"
	f, err := openProtected(path, 8192, 0644)
	if err != nil {
		return a, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(data) > 8192 || update.StrictJSON(data, &a) != nil || a.Version != 1 || a.Owner != owner || a.Group <= 0 || !filepath.IsAbs(a.Home) || filepath.Clean(a.Home) != a.Home || a.Home == "/" || a.Home == "/root" || (a.Profile != "local" && a.Profile != "remote") {
		return a, errors.New("invalid protected maintenance authority")
	}
	hash, err := hex.DecodeString(a.ImageSHA256)
	if err != nil || len(hash) != 32 || hex.EncodeToString(hash) != a.ImageSHA256 {
		return a, errors.New("invalid protected maintenance image")
	}
	return a, nil
}

func openProtected(path string, max int64, mode os.FileMode) (*os.File, error) {
	return update.OpenProtected(path, max, mode)
}

func VerifyImage(a Authority) error {
	f, err := openProtected(ImagePath(a.ImageSHA256), 128<<20, 0555)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, io.LimitReader(f, (128<<20)+1), make([]byte, 32*1024)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != a.ImageSHA256 {
		return errors.New("protected maintenance image changed")
	}
	return nil
}

func Call(ctx context.Context, r Request, diagnostics io.Writer) ([]byte, error) {
	if r.Action == "custody-enrollment-image" {
		return nil, errors.New("enrollment image requires its bounded stream")
	}
	c, b, err := requestCommand(ctx, r, diagnostics)
	if err != nil {
		return nil, err
	}
	c.Stdin = bytes.NewReader(b)
	var result boundedResponse
	c.Stdout = &result
	if err := c.Run(); err != nil {
		return nil, err
	}
	var response Response
	if update.StrictJSON(result.data, &response) != nil || response.Version != 1 || response.Action != r.Action || !response.Complete {
		return nil, errors.New("maintenance result is unconfirmed; preserve the operation")
	}
	return result.data, nil
}

// EnrollmentStreamCommand selects the same protected helper entry, but its
// fixed image operation carries the existing private image/lock stream.
func EnrollmentStreamCommand(ctx context.Context, r Request, diagnostics io.Writer) (*exec.Cmd, []byte, error) {
	if r.Action != "custody-enrollment-image" {
		return nil, nil, errors.New("fixed enrollment image operation required")
	}
	c, b, err := requestCommand(ctx, r, diagnostics)
	if err != nil {
		return nil, nil, err
	}
	frame := append([]byte(EnrollmentStreamPrefix), b...)
	frame = append(frame, '\n')
	return c, frame, nil
}

func requestCommand(ctx context.Context, r Request, diagnostics io.Writer) (*exec.Cmd, []byte, error) {
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return nil, nil, errors.New("maintenance requires the ordinary installation owner")
	}
	a, err := ReadAuthority(os.Getuid())
	if err != nil {
		return nil, nil, err
	}
	config, err := os.UserConfigDir()
	if err != nil || config != filepath.Join(a.Home, ".config") {
		return nil, nil, errors.New("maintenance setup is bound to this owner's standard configuration directory")
	}
	if err = VerifyImage(a); err != nil {
		return nil, nil, err
	}
	b, err := jsonRequest(r)
	if err != nil {
		return nil, nil, err
	}
	c := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "--", ImagePath(a.ImageSHA256), "maintenance-request", "--owner", strconv.Itoa(a.Owner))
	c.Dir = "/"
	c.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + a.Home, "LANG=C"}
	c.Stderr = diagnostics
	return c, b, nil
}

type boundedResponse struct{ data []byte }

func (b *boundedResponse) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > 64<<10 {
		return 0, errors.New("oversized maintenance response")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func jsonRequest(r Request) ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	_, err = DecodeRequest(b)
	return b, err
}

// ValidateProcess applies only to the installed helper's root request entry.
// A copied worktree binary or caller-controlled environment cannot opt in.
func ValidateProcess(owner int) (Authority, error) {
	var a Authority
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return a, errors.New("protected maintenance entry requires administrator execution")
	}
	a, err := ReadAuthority(owner)
	if err != nil {
		return a, err
	}
	if err := ValidateOwnerIdentity(a); err != nil {
		return a, err
	}
	path, err := os.Executable()
	if err != nil || path != ImagePath(a.ImageSHA256) {
		return a, errors.New("use the protected installed maintenance helper")
	}
	if caller := os.Getenv("SUDO_UID"); caller != "" && caller != strconv.Itoa(a.Owner) && caller != "0" {
		return a, errors.New("maintenance caller does not own this installation")
	}
	return a, VerifyImage(a)
}

func ValidateOwnerIdentity(a Authority) error {
	u, err := user.LookupId(strconv.Itoa(a.Owner))
	if err != nil || u.Gid != strconv.Itoa(a.Group) || u.HomeDir != a.Home {
		return errors.New("maintenance owner identity changed")
	}
	return nil
}
