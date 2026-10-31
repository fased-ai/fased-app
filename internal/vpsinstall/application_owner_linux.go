//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strconv"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

const applicationOwnerResponseLimit = 64 << 10

type applicationOwnerHelper func(context.Context, []byte, []string) ([]byte, error)

// ApplicationOwnerCall carries only update metadata to the immutable app-UID
// client. Interactive administrator authority drops UID/groups/capabilities
// before Fased executes; it grants no persistent or automatic-update authority.
func ApplicationOwnerCall(ctx context.Context, m Manifest, command core.ApplicationOwnerCommand, s *custodyinstall.System) (core.ApplicationOwnerView, error) {
	if s == nil {
		return core.ApplicationOwnerView{}, errors.New("ordinary Remote owner terminal authorization required")
	}
	if s.Authorization == "" {
		s.Authorization = "Authorize this Remote update metadata request through the dedicated app account. Fased remains unprivileged; no automatic-update authority is granted."
	}
	o := liveOps(s)
	return applicationOwnerCall(ctx, m, command, o, func(ctx context.Context, input []byte, args []string) ([]byte, error) {
		// Authorize with the original owner input, never the metadata payload.
		if _, e := s.Run(ctx, nil, "/usr/bin/setpriv", "--version"); e != nil {
			return nil, e
		}
		c := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "--"}, args...)...)
		c.Dir = "/"
		c.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
		c.Stdin = bytes.NewReader(input)
		var response applicationOwnerResponse
		c.Stdout = &response
		c.Stderr = s.Out
		if e := c.Run(); e != nil {
			return nil, e
		}
		return response.Bytes(), nil
	})
}

func applicationOwnerCall(ctx context.Context, m Manifest, command core.ApplicationOwnerCommand, o hostOps, helper applicationOwnerHelper) (core.ApplicationOwnerView, error) {
	var result core.ApplicationOwnerView
	if e := ctx.Err(); e != nil {
		return result, e
	}
	if helper == nil || !update.IDOK(command.ID) || command.Lease != "" && !update.IDOK(command.Lease) {
		return result, errors.New("one exact Remote owner operation required")
	}
	switch command.Action {
	case "reader-prepare", "reader-bootstrap", "reader-resume":
		if command.Receipt != nil {
			return result, errors.New("reader handoff cannot supply an outcome")
		}
	case "offline":
		if command.Lease != "" || command.Receipt != nil {
			return result, errors.New("offline owner inspection cannot create or change authority")
		}
	case "inspect", "reject":
		if command.Receipt != nil {
			return result, errors.New("owner inspection/rejection cannot carry an outcome")
		}
	case "claim", "prepare":
		if command.Lease == "" || command.Receipt != nil {
			return result, errors.New("one exact owner lease required")
		}
	case "finish":
		if command.Lease == "" || command.Receipt == nil || command.Receipt.ID != command.ID {
			return result, errors.New("one exact owner outcome required")
		}
	default:
		return result, errors.New("unsupported Remote owner operation")
	}
	reader := command.Action == "reader-prepare" || command.Action == "reader-bootstrap" || command.Action == "reader-resume"
	if !reader && command.ReaderBootstrap != nil {
		return result, errors.New("ordinary owner command cannot carry a reader transition")
	}
	input, e := json.Marshal(command)
	limit := 4096
	if reader {
		limit = 16384
	}
	if e != nil || len(input) > limit {
		return result, errors.New("oversized Remote owner command")
	}
	// All owner/configuration/source checks precede the first elevated utility.
	if e = ownedBy(o, m); e != nil {
		return result, e
	}
	image, e := applicationOwnerImage(o, m, command.ID)
	if reader {
		image, e = applicationReaderOwnerImage(o, m, command.ID)
	}
	if e != nil {
		return result, e
	}
	if e = loaded(ctx, o, m); e != nil {
		return result, e
	}
	for _, tool := range []string{"/usr/bin/sudo", "/usr/bin/setpriv", "/usr/bin/env"} {
		if e = trustedTool(o, tool); e != nil {
			return result, e
		}
	}
	// This final account check may ask the administrator to authorize the
	// fixed password-lock inspection, after code/configuration/tool validation.
	if e = verifyAccount(ctx, o, m); e != nil {
		return result, e
	}
	response, e := helper(ctx, input, applicationOwnerHelperArgs(m, image))
	if e != nil {
		return result, e
	}
	if update.StrictJSONBounded(response, &result, applicationOwnerResponseLimit) != nil || result.View.Version != 1 || result.View.InstanceID == "" || result.View.Operation.ID != command.ID {
		return core.ApplicationOwnerView{}, errors.New("Remote owner response is invalid or belongs to another operation")
	}
	return result, nil
}

func applicationOwnerArgs(m Manifest) []string {
	return applicationOwnerHelperArgs(m, m.Core)
}
func applicationOwnerHelperArgs(m Manifest, image string) []string {
	return []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(m.AppUID), "--regid", strconv.Itoa(m.AppGID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", "/usr/bin/env", "--ignore-environment", "--chdir=/", "HOME=/nonexistent", "PATH=/usr/bin:/bin", image, "application-owner-client", "--config", m.Config}
}

// Do not embed bytes.Buffer: its promoted ReadFrom would bypass Write's cap
// when os/exec copies helper stdout through io.Copy.
type applicationOwnerResponse struct{ buffer bytes.Buffer }

func (b *applicationOwnerResponse) Bytes() []byte { return b.buffer.Bytes() }
func (b *applicationOwnerResponse) Len() int      { return b.buffer.Len() }

func (b *applicationOwnerResponse) Write(p []byte) (int, error) {
	if len(p) > applicationOwnerResponseLimit-b.Len() {
		return 0, errors.New("oversized Remote owner response")
	}
	return b.buffer.Write(p)
}

var _ io.Writer = (*applicationOwnerResponse)(nil)
