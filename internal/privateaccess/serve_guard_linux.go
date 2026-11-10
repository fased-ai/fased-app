//go:build linux

package privateaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/core"
)

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
	var plans []Plan
	if r, e := load(dir); e == nil {
		plans = append(plans, r.Plan)
		if r.Next != nil {
			plans = append(plans, *r.Next)
		}
	} else if !os.IsNotExist(e) {
		return false
	}
	if data, e := core.ReadPrivateFile(filepath.Join(dir, "private-setup.json")); e == nil {
		var r preflightReceipt
		if json.Unmarshal(data, &r) != nil || r.Version != 1 || r.Plan.Mode != "tailscale" || !validTarget(r.Plan.Target) {
			return false
		}
		plans = append(plans, r.Plan)
	} else if !os.IsNotExist(e) {
		return false
	}
	return uid > 0 && allowedOwnerServeDelta(input, args, current, currentTag, plans, func(p Plan) bool { return owns(p, uid) })
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
	decode := func(data []byte) (map[string]any, bool) {
		var result map[string]any
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		if d.Decode(&result) != nil || result == nil || d.Decode(new(any)) != io.EOF {
			return nil, false
		}
		for k, v := range result {
			if v == nil {
				delete(result, k)
			} else if m, ok := v.(map[string]any); ok && len(m) == 0 {
				delete(result, k)
			}
		}
		return result, true
	}
	before, ok := decode(current)
	if !ok {
		return false
	}
	after, ok := decode(input)
	if !ok {
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
