//go:build linux

package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// ValidateProtectedRemoteSetup binds installation to the enrolled device and
// the owner's recorded wizard plan. It does not publish a route or close ports.
func ValidateProtectedRemoteSetup(ctx context.Context, owner int, home, listen, origin string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 {
		return errors.New("protected Remote setup required")
	}
	m := Manager{Run: func(ctx context.Context, args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, "/usr/bin/tailscale", args...)
		c.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
		return c.Output()
	}}
	p, err := m.Plan(ctx, "tailscale", listen)
	if err != nil {
		return err
	}
	u, err := url.Parse(origin)
	expected, e := url.Parse(p.Origin)
	if err != nil || e != nil || u.Scheme != "https" || u.Hostname() != expected.Hostname() {
		return errors.New("Remote installation requires this enrolled device's private HTTPS")
	}
	plans, err := ownerServePlans(filepath.Join(home, ".config", "fased-app", "vps-access"), func(path string) ([]byte, error) { return readOwnerServeFile(path, owner) })
	if err != nil {
		return err
	}
	for _, record := range plans {
		if record.Mode == "tailscale" && record.Origin == origin && (record.Target == "http://"+listen || ownedServeTarget(record, owner)) {
			return nil
		}
	}
	return errors.New("Remote installation differs from its recorded private setup")
}

// AllowedOwnedServeCommand runs before sudo. The daemon's conditional tag
// prevents a concurrent change between this inspection and the OS write.
func AllowedOwnedServeCommand(ctx context.Context, input []byte, args []string) bool {
	dir, err := core.DefaultDataDir()
	if err != nil {
		return false
	}
	return allowedOwnedServeCommand(ctx, input, args, systemConfig, filepath.Join(dir, "vps-access"), os.Getuid(), ownedServeTarget)
}

func allowedOwnedServeCommand(ctx context.Context, input []byte, args []string, getConfig func(context.Context) ([]byte, string, error), dir string, uid int, owns func(Plan, int) bool) bool {
	if len(args) != len(serveCurlPrefix)+6 || !strings.HasPrefix(args[len(serveCurlPrefix)], "If-Match: ") {
		return false
	}
	tag := strings.TrimPrefix(args[len(serveCurlPrefix)], "If-Match: ")
	expected, err := serveRawCommand(input, tag)
	if err != nil || !slices.Equal(args, expected) {
		return false
	}
	current, currentTag, err := getConfig(ctx)
	if err != nil || currentTag != tag {
		return false
	}
	plans, err := ownerServePlans(dir, core.ReadPrivateFile)
	if err != nil {
		return false
	}
	return uid > 0 && allowedOwnerServeDelta(input, args, current, currentTag, plans, func(p Plan) bool { return owns(p, uid) })
}

func ownerServePlans(dir string, read func(string) ([]byte, error)) ([]Plan, error) {
	var plans []Plan
	if data, e := read(filepath.Join(dir, "private-access.json")); e == nil {
		r, e := parseReceipt(data)
		if e != nil {
			return nil, e
		}
		plans = append(plans, r.Plan)
		if r.Next != nil {
			plans = append(plans, *r.Next)
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if data, e := read(filepath.Join(dir, "private-setup.json")); e == nil {
		var r preflightReceipt
		if update.StrictJSON(data, &r) != nil || r.Version != 1 || r.Plan.Mode != "tailscale" || !validTarget(r.Plan.Target) {
			return nil, errors.New("invalid owned private preflight receipt")
		}
		plans = append(plans, r.Plan)
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return plans, nil
}

// ApplyOwnedServe is called only by the protected maintenance entry. It repeats
// ownership, body and daemon-tag validation inside the privileged process and
// uses the local API directly; no elevated curl/stdin grant is involved.
func ApplyOwnedServe(ctx context.Context, owner int, home string, data []byte, etag string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 || !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" {
		return errors.New("protected private-access maintenance required")
	}
	dir := filepath.Join(home, ".config", "fased-app", "vps-access")
	plans, err := ownerServePlans(dir, func(path string) ([]byte, error) { return readOwnerServeFile(path, owner) })
	if err != nil {
		return err
	}
	return applyOwnedServe(ctx, owner, plans, data, etag, systemConfig, systemSetConfig)
}

func applyOwnedServe(ctx context.Context, owner int, plans []Plan, data []byte, etag string, get func(context.Context) ([]byte, string, error), set func(context.Context, []byte, string) error) error {
	args, err := serveRawCommand(data, etag)
	if err != nil || owner <= 0 {
		return errors.New("invalid owned private Serve request")
	}
	current, tag, err := get(ctx)
	if err != nil {
		return err
	}
	if !allowedOwnerServeDelta(data, args, current, tag, plans, func(p Plan) bool { return ownedServeTarget(p, owner) }) {
		return errors.New("private Serve authority changed or request modifies an unowned route")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return set(ctx, data, etag)
}

func readOwnerServeFile(path string, owner int) ([]byte, error) {
	// The directory is owner-controlled; bind every component against symlink
	// traversal and validate the held file's actual identity before reading it.
	for p := filepath.Dir(path); p != "/"; p = filepath.Dir(p) {
		i, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 || st.Uid != 0 && int(st.Uid) != owner {
			return nil, errors.New("private Serve receipt has foreign parents")
		}
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return nil, err
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != owner || st.Nlink != 1 || !i.Mode().IsRegular() || i.Mode() != 0600 || i.Size() < 0 || i.Size() > 16<<10 {
		return nil, errors.New("private Serve receipt has foreign ownership or size")
	}
	b, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil || int64(len(b)) != i.Size() {
		return nil, errors.New("private Serve receipt changed while reading")
	}
	return b, nil
}

func allowedOwnerServeDelta(input []byte, args []string, current []byte, currentTag string, plans []Plan, owns func(Plan) bool) bool {
	if len(args) != len(serveCurlPrefix)+6 || !strings.HasPrefix(args[len(serveCurlPrefix)], "If-Match: ") {
		return false
	}
	tag := strings.TrimPrefix(args[len(serveCurlPrefix)], "If-Match: ")
	expected, err := serveRawCommand(input, tag)
	if err != nil || !slices.Equal(args, expected) || currentTag != tag {
		return false
	}
	before, err := decodeServeInventory(current, true)
	if err != nil {
		return false
	}
	after, err := decodeServeInventory(input, false)
	if err != nil {
		return false
	}
	return routeDeltaAllowed(before, after, plans, owns)
}

func ownedServeTarget(p Plan, uid int) bool {
	if _, _, err := routeParts(p); err != nil {
		return false
	}
	for _, kind := range []string{"vps", "custody"} {
		if p.Target == fmt.Sprintf("unix:/var/lib/fased-app-%s-%d/app-state/tailscale.sock", kind, uid) {
			return true
		}
	}
	if strings.HasPrefix(p.Target, "unix:/tmp/fased-private-") {
		socket := strings.TrimPrefix(p.Target, "unix:")
		dir := filepath.Dir(socket)
		if filepath.Dir(dir) != "/tmp" || filepath.Base(socket) != "tailscale.sock" {
			return false
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		return ok && int(st.Uid) == uid
	}
	if !strings.HasPrefix(p.Target, "http://127.0.0.1:") {
		return false
	}
	// A legacy loopback handoff is owned only when the immutable root-managed
	// installation manifest binds that exact owner, listener and private origin.
	root := fmt.Sprintf("/var/lib/fased-app-vps-%d", uid)
	for _, path := range []string{"/var", "/var/lib", root, filepath.Join(root, "install.json")} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 {
			return false
		}
		if path != filepath.Join(root, "install.json") && !info.IsDir() {
			return false
		}
		if path == filepath.Join(root, "install.json") && (!info.Mode().IsRegular() || info.Size() > 65536) {
			return false
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "install.json"))
	if err != nil {
		return false
	}
	var m struct {
		Version      int    `json:"version"`
		Phase        string `json:"phase"`
		OwnerUID     int    `json:"ownerUid"`
		Root         string `json:"root"`
		Listen       string `json:"listen"`
		PublicOrigin string `json:"publicOrigin"`
	}
	return json.Unmarshal(data, &m) == nil && m.Version == 1 && m.Phase == "installed" && m.OwnerUID == uid && m.Root == root && p.Target == "http://"+m.Listen && p.Origin == m.PublicOrigin
}
