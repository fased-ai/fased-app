package core

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net/http"
)

type ApplicationOwnerCommand struct {
	ReaderBootstrap *ApplicationReaderBootstrapRequest `json:"readerBootstrap,omitempty"`
	Action          string                             `json:"action"`
	ID              string                             `json:"id"`
	Lease           string                             `json:"lease,omitempty"`
	Receipt         *ApplicationUpdateReceipt          `json:"receipt,omitempty"`
}
type ApplicationOwnerView struct {
	ReaderBootstrap *ApplicationReaderBootstrapRequest `json:"readerBootstrap,omitempty"`
	RunningSHA256   string                             `json:"runningSha256"`
	BootID          string                             `json:"bootId"`
	View            ApplicationUpdateView              `json:"view"`
	MayApply        bool                               `json:"mayApply"`
	Checkpoint      *ApplicationCheckpoint             `json:"checkpoint,omitempty"`
}

// Only the OS-protected IPC socket admits this protocol. The app OS identity
// already owns these metadata; it gets no OS replacement or signer authority.
// The separate owner actor authenticates releases and owns installation/health.
// No endpoint accepts artifacts, paths, credentials or permission overrides.
func (h *HTTPHandler) applicationOwnerAPI(w http.ResponseWriter, r *http.Request) {
	if r.Context().Value(privateIPCKey{}) != true {
		writeError(w, 403, "protected owner maintenance process required")
		return
	}
	if !h.mutation(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	var c ApplicationOwnerCommand
	if e != nil || update.StrictJSON(b, &c) != nil || !update.IDOK(c.ID) || c.ReaderBootstrap != nil {
		writeError(w, 400, "one exact update operation required")
		return
	}
	v, e := h.store.ApplicationOwnerCommand(r.Context(), c)
	if e != nil {
		writeError(w, 409, "owner maintenance refused; preserve the transaction")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Store) ApplicationOwnerCommand(ctx context.Context, c ApplicationOwnerCommand) (ApplicationOwnerView, error) {
	s.appUpdates.mu.Lock()
	defer s.appUpdates.mu.Unlock()
	v, e := s.ApplicationUpdates(ctx)
	if e != nil {
		return ApplicationOwnerView{}, e
	}
	o := v.Operation
	if o.ID != c.ID || o.Release == nil {
		return ApplicationOwnerView{}, errors.New("unknown owner operation")
	}
	running, _ := runningApplicationSHA256()
	out := ApplicationOwnerView{View: v, RunningSHA256: running, BootID: s.bootID}
	switch c.Action {
	case "inspect":
		if (c.Lease != "" && c.Lease != o.OwnerLease) || c.Receipt != nil {
			return out, errors.New("inspection cannot change authority")
		}
		return out, nil
	case "claim":
		if !update.IDOK(c.Lease) || c.Receipt != nil || o.Phase != "applying" {
			return out, errors.New("one dispatched owner lease required")
		}
		if o.OwnerLease != "" {
			return out, nil
		} // after interruption inspect/status, never resubmit.
		if o.PolicySHA256 != update.SnapshotDigest("application-update-policy", v.Policy) || (o.Automatic && (v.Policy.Mode != "automatic" || v.Policy.Window != nil && !v.Policy.Window.Contains(s.now()))) {
			o.Phase = "available"
			o.Failure = "Update deferred until the approved policy allows it."
			if e = s.publishApplicationUpdate(ctx, o, v.Revision); e != nil {
				return out, e
			}
			out.View, e = s.ApplicationUpdates(ctx)
			return out, e
		}
		o.OwnerLease = c.Lease
		if e = s.publishApplicationUpdate(ctx, o, v.Revision); e != nil {
			return out, e
		}
		out.View, e = s.ApplicationUpdates(ctx)
		out.MayApply = e == nil
		return out, e
	case "prepare":
		if c.Lease == "" || c.Lease != o.OwnerLease || !oneOf(o.Phase, "applying", "recovery") || c.Receipt != nil {
			return out, errors.New("exact owner lease required")
		}
		checkpoint, e := s.PrepareApplicationCheckpoint(ctx, c.ID)
		if e != nil {
			return out, e
		}
		out.Checkpoint = &checkpoint
		return out, nil
	case "reject":
		if c.Receipt != nil || (o.OwnerLease != "" && c.Lease != o.OwnerLease) {
			return out, errors.New("exact owner rejection required")
		}
		var phase string
		if e = s.db.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil || phase == "paused" {
			return out, errors.New("held transaction cannot be rejected as unsent")
		}
		o.Phase = "check-failed"
		o.CheckedAt = s.now().Unix()
		o.Failure = "Release is no longer eligible. Check for updates again."
		if e = s.publishApplicationUpdate(ctx, o, v.Revision); e != nil {
			return out, e
		}
		out.View, e = s.ApplicationUpdates(ctx)
		return out, e
	case "finish":
		if c.Receipt != nil && o.Receipt != nil && c.Lease == o.OwnerLease && update.SnapshotDigest("application-outcome", *c.Receipt) == update.SnapshotDigest("application-outcome", *o.Receipt) && oneOf(o.Phase, "completed", "rolled-back") {
			return out, nil
		}
		if c.Lease == "" || c.Lease != o.OwnerLease || c.Receipt == nil || !oneOf(o.Phase, "applying", "recovery") {
			return out, errors.New("exact owner outcome required")
		}
		proof := *c.Receipt
		if proof.ID != o.ID || proof.Sequence != o.Release.Sequence || proof.ManifestSHA256 != o.Release.ManifestSHA256 || !oneOf(proof.Phase, "completed", "rolled-back", "recovery") {
			return out, errors.New("owner receipt differs from operation")
		}
		if proof.Phase != "recovery" {
			if e = s.CompleteApplicationCheckpoint(ctx, c.ID, proof); e != nil {
				return out, e
			}
			out.View, e = s.ApplicationUpdates(ctx)
			return out, e
		}
		o.Receipt = &proof
		o.Phase = proof.Phase
		o.FinishedAt = s.now().Unix()
		if proof.Phase == "recovery" {
			o.Failure = "Update requires protected recovery."
		} else {
			o.Failure = ""
		}
		if e = s.publishApplicationUpdate(ctx, o, v.Revision); e != nil {
			return out, e
		}
		out.View, e = s.ApplicationUpdates(ctx)
		return out, e
	default:
		return out, errors.New("unsupported owner operation")
	}
}

// BindApplicationMaintenance connects the existing core worker to an OS owner
// actor. Apply only launches the fixed installed entry, never swaps its own
// running/sandboxed executable. The owner publishes its durable receipt via IPC.
func (s *Store) BindApplicationMaintenance(check func(context.Context) (*ApplicationRelease, error), launch func(context.Context, string) error, asynchronous ...bool) error {
	if check == nil || launch == nil {
		return errors.New("authenticated source and fixed owner actor required")
	}
	return s.BindApplicationUpdateController(&applicationMaintenanceProxy{store: s, check: check, launch: launch}, asynchronous...)
}

type applicationMaintenanceProxy struct {
	store  *Store
	check  func(context.Context) (*ApplicationRelease, error)
	launch func(context.Context, string) error
}

func (p *applicationMaintenanceProxy) Check(ctx context.Context) (*ApplicationRelease, error) {
	return p.check(ctx)
}
func (p *applicationMaintenanceProxy) Apply(ctx context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	if e := p.launch(ctx, o.ID); e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	return ApplicationUpdateReceipt{ID: o.ID, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, Phase: "applying"}, nil
}
func (p *applicationMaintenanceProxy) Status(ctx context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	current, _, e := readApplicationUpdate(ctx, p.store.db)
	if e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	if current.ID != o.ID {
		return ApplicationUpdateReceipt{}, errors.New("owner operation changed")
	}
	if current.Receipt != nil {
		return *current.Receipt, nil
	}
	// Persistent owner template resumes after app stop/reboot. Re-requesting the
	// exact active unit cannot launch another instance; it never retries an install.
	if e = p.launch(ctx, o.ID); e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	return ApplicationUpdateReceipt{ID: o.ID, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, Phase: "applying"}, nil
}
