//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestBoundUnitWriteBindsBytesAndOwnedDestination(t *testing.T) {
	id := strings.Repeat("a", 32)
	stage := "/etc/systemd/system/.fased-custody-1900-app.service-" + id + ".pending"
	body := []byte("[Service]\nUser=991\nNoNewPrivileges=yes\n")
	a, err := BoundUnitWriteArgs(1900, stage, body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BoundUnitWriteArgs(1900, stage, append([]byte(nil), append(body, 'x')...))
	if err != nil || slices.Equal(a, b) {
		t.Fatal("different service bytes were not bound to the OS command", err)
	}
	if !AllowedBoundUnitWrite(1900, body, a) || AllowedBoundUnitWrite(1900, append(body, 'x'), a) {
		t.Fatal("bound service write accepted substituted input")
	}
	decoded, err := exec.Command(a[10], a[11:]...).Output()
	if err != nil || !bytes.Equal(decoded, body) {
		t.Fatal("the fixed OS writer changed the approved service bytes", err)
	}
	changed := append([]string(nil), a...)
	changed[len(changed)-1] += "x"
	if AllowedBoundUnitWrite(1900, body, changed) {
		t.Fatal("altered privileged command was accepted")
	}
	for _, p := range []string{
		"/etc/systemd/system/.fased-custody-1000-app.service-" + id + ".pending",
		"/etc/systemd/system/.sshd.service-" + id + ".pending",
		"/etc/systemd/system/../.fased-custody-1900-app.service-" + id + ".pending",
		"/var/lib/.fased-app-vps-1900-" + id + ".pending/.fased-vps-1900.service-" + strings.Repeat("b", 32) + ".pending",
	} {
		if _, err := BoundUnitWriteArgs(1900, p, body); err == nil {
			t.Fatal("foreign destination accepted", p)
		}
	}
	dropIn := "/etc/systemd/system/fased-vps-1900.service.d/.30-fased-provider-credentials.conf-" + id + ".pending"
	if args, err := BoundUnitWriteArgs(1900, dropIn, body); err != nil || !AllowedBoundUnitWrite(1900, body, args) || AllowedBoundUnitWrite(1900, append(bytes.Clone(body), 'x'), args) {
		t.Fatal("owned credential override must bind its exact bytes", err)
	}
	vps := "/var/lib/.fased-app-vps-1900-" + id + ".pending/.fased-vps-1900.service-" + id + ".pending"
	if args, err := BoundUnitWriteArgs(1900, vps, body); err != nil || !AllowedBoundUnitWrite(1900, body, args) {
		t.Fatal("owned Remote installer stage refused", err)
	}
	if _, err := BoundUnitWriteArgs(1900, stage, make([]byte, 8193)); err == nil {
		t.Fatal("oversized service content accepted")
	}
}

func TestBoundPolicyWriteBindsBytesAndOwner(t *testing.T) {
	id := strings.Repeat("a", 32)
	stage := "/etc/sudoers.d/.fased-remote-proof-1900-" + id + ".pending"
	body := []byte("# fixed policy\n#1900 ALL=(root) NOPASSWD: /usr/bin/true\n")
	args, err := BoundPolicyWriteArgs(1900, stage, body)
	if err != nil || !AllowedBoundPolicyWrite(1900, body, args) {
		t.Fatal("owned policy write refused", err)
	}
	if args[6] != "--property=UMask=0227" {
		t.Fatal("sudo policy staging is not restricted to mode 0440")
	}
	decoded, err := exec.Command(args[10], args[11:]...).Output()
	if err != nil || !bytes.Equal(decoded, body) {
		t.Fatal("fixed OS writer changed the approved policy bytes", err)
	}
	if AllowedBoundPolicyWrite(1900, append(bytes.Clone(body), 'x'), args) {
		t.Fatal("substituted sudo policy accepted")
	}
	if _, err := BoundPolicyWriteArgs(1900, strings.Replace(stage, "1900", "1000", 1), body); err == nil {
		t.Fatal("foreign sudo policy destination accepted")
	}
}

func TestBoundReusableSetupRejectsForeignTargetsAndBytes(t *testing.T) {
	body := []byte("fixed reusable setup")
	for _, name := range []string{"stage-image-retry@.service", "stage-meta-retry@.service", "stage-unit-retry@.service"} {
		for _, path := range []string{
			"/etc/systemd/system/fased-custody-1900-" + name,
			"/etc/systemd/system/.fased-custody-1900-" + name + "-" + strings.Repeat("a", 32) + ".pending",
		} {
			if _, err := BoundAtomicUnitWriteArgs(1900, path, body); err == nil {
				t.Fatal("obsolete parallel retry template accepted", path)
			}
		}
	}
	for _, name := range []string{"atomic-launcher.service", "atomic-provision.service", "atomic-compat.service", "stage-image@.service", "stage-meta@.service", "stage-unit@.service"} {
		path := "/etc/systemd/system/fased-custody-1900-" + name
		args, err := BoundAtomicUnitWriteArgs(1900, path, body)
		if err != nil || !AllowedBoundAtomicUnitWrite(1900, body, args) || AllowedBoundAtomicUnitWrite(1900, append(bytes.Clone(body), 'x'), args) {
			t.Fatal("reusable service bytes were not bound", name, err)
		}
		pending := "/etc/systemd/system/.fased-custody-1900-" + name + "-" + strings.Repeat("a", 32) + ".pending"
		if args, err := BoundAtomicUnitWriteArgs(1900, pending, body); err != nil || !AllowedBoundAtomicUnitWrite(1900, body, args) {
			t.Fatal("reusable service staging refused", name, err)
		}
	}
	for _, name := range []string{"authority.json", "reusable-v1.json", "guard.sha256", "stage-image.allow", "stage-meta.allow", "stage-unit.allow"} {
		path := Root(1900) + "/maintenance/." + name + "-" + strings.Repeat("a", 32) + ".pending"
		args, err := BoundReusableMetadataWriteArgs(1900, path, body)
		if err != nil || !AllowedBoundReusableMetadataWrite(1900, body, args) || AllowedBoundReusableMetadataWrite(1900, append(bytes.Clone(body), 'x'), args) {
			t.Fatal("reusable metadata bytes were not bound", name, err)
		}
	}
	for _, path := range []string{Root(1901) + "/maintenance/.authority.json-" + strings.Repeat("a", 32) + ".pending", Root(1900) + "/maintenance/authority.json", Root(1900) + "/maintenance/other", Root(1900) + "/maintenance/../identity.json"} {
		if _, err := BoundReusableMetadataWriteArgs(1900, path, body); err == nil {
			t.Fatal("foreign metadata destination accepted", path)
		}
	}
	pending := "/etc/sudoers.d/.fased-custody-1900-reusable-" + strings.Repeat("a", 32) + ".pending"
	if args, err := BoundPolicyWriteArgs(1900, pending, body); err != nil || !AllowedBoundPolicyWrite(1900, body, args) {
		t.Fatal("exact reusable policy staging refused", err)
	}
}

func TestBoundReusableSetupNeverOverwritesExistingInboxOrImage(t *testing.T) {
	owner, id := 1900, strings.Repeat("a", 32)
	inbox, err := boundReusableInboxCreateArgs(owner, 997, id)
	if err != nil || !allowedBoundReusableInboxCreate(owner, inbox) || !strings.Contains(inbox[6], "ConditionPathExists=!") {
		t.Fatal("inbox creation is not bound to an absent path", err)
	}
	changed := slices.Clone(inbox)
	changed[6] = "--property=ConditionPathExists=/tmp/foreign"
	if allowedBoundReusableInboxCreate(owner, changed) || allowedBoundReusableInboxCreate(owner+1, inbox) {
		t.Fatal("foreign or unconditional inbox creation accepted")
	}
	for _, component := range []string{"runner", "guard"} {
		args, err := boundReusableImageCopyArgs(owner, id, component, strings.Repeat("b", 64))
		if err != nil || !allowedBoundReusableImageCopy(owner, args) || !strings.Contains(args[6], "ConditionPathExists=!") || !strings.Contains(args[7], "ConditionPathExists=!") || !strings.Contains(args[12], "--no-target-directory") {
			t.Fatal("image copy is not bound to an absent staging path", component, err)
		}
		changed = slices.Clone(args)
		changed[8] = "--property=StandardInput=file:/tmp/foreign"
		if allowedBoundReusableImageCopy(owner, changed) {
			t.Fatal("foreign image source accepted")
		}
	}
}

func TestReusableSetupPublicationRecoversOnlyItsOwnPartialCopy(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	body := []byte("exact reusable protected file")
	for _, item := range []struct {
		name, target string
		args         func(int, string, []byte) ([]string, error)
	}{
		{"metadata", Root(owner) + "/maintenance/authority.json", BoundReusableMetadataWriteArgs},
		{"unit", "/etc/systemd/system/fased-custody-" + strconv.Itoa(owner) + "-atomic-launcher.service", BoundAtomicUnitWriteArgs},
	} {
		t.Run(item.name, func(t *testing.T) {
			for cut := 1; cut <= 5; cut++ {
				h := &memoryHost{files: map[string]hostFile{}, failAfter: cut}
				_ = publishReusableBound(t.Context(), owner, id, item.target, body, h.read, h.run, item.args)
				h.failAfter = 0
				if err := publishReusableBound(t.Context(), owner, id, item.target, body, h.read, h.run, item.args); err != nil {
					t.Fatalf("interruption %d did not resume: %v", cut, err)
				}
				if f, err := h.read(item.target); err != nil || !known(f, body, 0644) {
					t.Fatal("exact protected file was not published", err)
				}
			}
			pending := filepath.Join(filepath.Dir(item.target), "."+filepath.Base(item.target)+"-"+id+".pending")
			h := &memoryHost{files: map[string]hostFile{pending: {UID: 0, Mode: 0644, Regular: true, Data: bytes.Clone(body[:5])}}}
			if err := publishReusableBound(t.Context(), owner, id, item.target, body, h.read, h.run, item.args); err != nil {
				t.Fatal("classified partial copy did not resume", err)
			}
			h = &memoryHost{files: map[string]hostFile{pending: {UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign")}}}
			if err := publishReusableBound(t.Context(), owner, id, item.target, body, h.read, h.run, item.args); err == nil || h.calls != 0 {
				t.Fatal("foreign staging was changed")
			}
		})
	}
}

func TestReusablePublicationUsesGuardedOSOperation(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	target := Root(owner) + "/maintenance/authority.json"
	body := []byte("exact reusable protected file")
	h := &memoryHost{files: map[string]hostFile{}}
	run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/ln" {
			return nil, errors.New("unverified direct publication")
		}
		return h.run(ctx, input, args...)
	}
	if err := publishReusableBound(t.Context(), owner, id, target, body, h.read, run, BoundReusableMetadataWriteArgs); err != nil {
		t.Fatal("protected publication did not use the guarded OS operation:", err)
	}
	if got, err := h.read(target); err != nil || !known(got, body, 0644) {
		t.Fatal("protected publication did not preserve exact bytes", err)
	}
}

func TestReusableCompletedCleanupUsesGuardedOSOperation(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	target := Root(owner) + "/maintenance/authority.json"
	body := []byte("exact reusable protected file")
	h := &memoryHost{files: map[string]hostFile{}}
	run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/rm" {
			return nil, errors.New("unverified direct cleanup")
		}
		return h.run(ctx, input, args...)
	}
	if err := publishReusableBound(t.Context(), owner, id, target, body, h.read, run, BoundReusableMetadataWriteArgs); err != nil {
		t.Fatal("completed staging did not use guarded cleanup:", err)
	}
	pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+id+".pending")
	if _, err := h.read(pending); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed staging was not cleaned", err)
	}
}

func TestBoundReusablePublicationBindsOwnerDestinationAndBytes(t *testing.T) {
	owner, id := 1900, strings.Repeat("a", 32)
	target := Root(owner) + "/maintenance/authority.json"
	pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+id+".pending")
	body := []byte("exact reusable protected file")
	args, err := boundReusableSetupLinkArgs(owner, id, pending, target, body)
	if err != nil || !allowedBoundReusableSetupLink(owner, body, args) {
		t.Fatal("exact guarded publication refused", err)
	}
	if allowedBoundReusableSetupLink(owner+1, body, args) || allowedBoundReusableSetupLink(owner, append(bytes.Clone(body), 'x'), args) {
		t.Fatal("changed owner or input accepted")
	}
	altered := slices.Clone(args)
	altered[len(altered)-2] = "/tmp/foreign"
	if allowedBoundReusableSetupLink(owner, body, altered) {
		t.Fatal("foreign publication destination accepted")
	}
	altered = slices.Clone(args)
	altered[25] = "/usr/bin/true"
	if allowedBoundReusableSetupLink(owner, body, altered) {
		t.Fatal("removed byte comparison accepted")
	}
	if _, err = boundReusableSetupLinkArgs(owner, id, pending, "/etc/systemd/system/sshd.service", body); err == nil {
		t.Fatal("foreign target accepted")
	}
}

func TestBoundReusableCompletedCleanupBindsInodeAndBytes(t *testing.T) {
	owner, id := 1900, strings.Repeat("a", 32)
	target := Root(owner) + "/maintenance/authority.json"
	pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+id+".pending")
	body := []byte("exact reusable protected file")
	args, err := boundReusableSetupCleanupArgs(owner, id, pending, target, body)
	if err != nil || !allowedBoundReusableSetupCleanup(owner, body, args) {
		t.Fatal("exact guarded cleanup refused", err)
	}
	link, err := boundReusableSetupLinkArgs(owner, id, pending, target, body)
	if err != nil || link[4] != args[4] || !slices.Contains(args, "-samefile") || !slices.Contains(args, "-links") {
		t.Fatal("cleanup is not inode-bound and serialized with publication", err)
	}
	changed := slices.Clone(args)
	changed[28] = "/tmp/foreign"
	if allowedBoundReusableSetupCleanup(owner, body, changed) || allowedBoundReusableSetupCleanup(owner, append(bytes.Clone(body), 'x'), args) {
		t.Fatal("changed cleanup target or bytes accepted")
	}
}

func TestReusablePendingCleanupRefusesDirectRemoval(t *testing.T) {
	owner, id := 1900, strings.Repeat("a", 32)
	for _, pending := range []string{
		Root(owner) + "/maintenance/.authority.json-" + id + ".pending",
		Root(owner) + "/maintenance/.runner-" + id + ".pending",
		Root(owner) + "/maintenance/.runner-" + id + ".pending.source",
		"/etc/systemd/system/.fased-custody-1900-atomic-launcher.service-" + id + ".pending",
		"/etc/systemd/system/fased-custody-1900-app.service.d/.20-fased-update-guard.conf-" + id + ".pending",
		"/etc/sudoers.d/.fased-custody-1900-reusable-" + id + ".pending",
	} {
		if err := refuseDirectReusablePendingRemoval(owner, []string{"/usr/bin/rm", "--", pending}); err == nil {
			t.Fatalf("direct reusable staging removal accepted: %s", pending)
		}
	}
	if err := refuseDirectReusablePendingRemoval(owner, []string{"/usr/bin/rm", "--", "/tmp/unrelated"}); err != nil {
		t.Fatal("unrelated cleanup was rejected", err)
	}
}

func TestSystemRunRefusesDirectReusablePendingRemoval(t *testing.T) {
	owner := os.Getuid()
	if owner <= 0 {
		t.Skip("ordinary installation owner required")
	}
	pending := Root(owner) + "/maintenance/.runner-" + strings.Repeat("a", 32) + ".pending"
	_, err := (&System{}).Run(t.Context(), nil, "/usr/bin/rm", "--", pending)
	if err == nil || !strings.Contains(err.Error(), "guarded serialized operation") {
		t.Fatal("actual OS entry did not refuse direct reusable staging removal", err)
	}
}

func TestBoundReusableImagePublicationChecksApprovedDigest(t *testing.T) {
	owner, id, digest := 1900, strings.Repeat("a", 32), strings.Repeat("b", 64)
	for _, component := range []string{"runner", "guard"} {
		args, err := boundReusableImageLinkArgs(owner, id, component, digest)
		if err != nil || !allowedBoundReusableImageLink(owner, args) {
			t.Fatalf("exact image publication refused: %v", err)
		}
		if !slices.Contains(args, "/usr/bin/sha256sum") || !slices.Contains(args, "/usr/bin/cmp") {
			t.Fatal("image publication does not verify the approved source and copy")
		}
		changed := slices.Clone(args)
		changed[len(changed)-2] = "/tmp/foreign"
		if allowedBoundReusableImageLink(owner, changed) || allowedBoundReusableImageLink(owner+1, args) {
			t.Fatal("changed image publication target or owner accepted")
		}
	}
}

func TestBoundReusableImageCleanupChecksInodeAndDigest(t *testing.T) {
	owner, id, digest := 1900, strings.Repeat("a", 32), strings.Repeat("b", 64)
	for _, component := range []string{"runner", "guard"} {
		args, err := boundReusableImageCleanupArgs(owner, id, component, digest)
		if err != nil || !allowedBoundReusableImageCleanup(owner, args) {
			t.Fatalf("exact guarded image cleanup refused: %v", err)
		}
		link, err := boundReusableImageLinkArgs(owner, id, component, digest)
		if err != nil || args[4] != link[4] || !slices.Contains(args, "-samefile") || !strings.Contains(strings.Join(args, "\x00"), "ExecStartPre=/usr/bin/sha256sum") {
			t.Fatal("image cleanup is not serialized and inode/digest bound", err)
		}
		changed := slices.Clone(args)
		changed[29] = "/tmp/foreign"
		if allowedBoundReusableImageCleanup(owner, changed) || allowedBoundReusableImageCleanup(owner+1, args) {
			t.Fatal("changed image cleanup target or owner accepted")
		}
	}
}

func TestReusableSetupSerializesWriterAndPublicationPerPendingFile(t *testing.T) {
	owner, id := 1900, strings.Repeat("a", 32)
	body := []byte("exact reusable protected file")
	for _, item := range []struct {
		target string
		writer func(int, string, []byte) ([]string, error)
	}{
		{Root(owner) + "/maintenance/authority.json", BoundReusableMetadataWriteArgs},
		{"/etc/systemd/system/fased-custody-1900-atomic-launcher.service", BoundAtomicUnitWriteArgs},
	} {
		pending := filepath.Join(filepath.Dir(item.target), "."+filepath.Base(item.target)+"-"+id+".pending")
		write, err := item.writer(owner, pending, body)
		if err != nil {
			t.Fatal(err)
		}
		link, err := boundReusableSetupLinkArgs(owner, id, pending, item.target, body)
		if err != nil || write[4] != link[4] {
			t.Fatalf("setup write and publication are not serialized for %s: %v", item.target, err)
		}
	}
	for _, component := range []string{"runner", "guard"} {
		copy, err := boundReusableImageCopyArgs(owner, id, component, strings.Repeat("b", 64))
		if err != nil {
			t.Fatal(err)
		}
		link, err := boundReusableImageLinkArgs(owner, id, component, strings.Repeat("b", 64))
		if err != nil || copy[4] != link[4] {
			t.Fatalf("image copy and publication are not serialized for %s: %v", component, err)
		}
	}
}

func TestReusableRestartAssertionStageIsNoClobber(t *testing.T) {
	id := strings.Repeat("a", 32)
	body := []byte("[Unit]\nAssertPathExists=!" + Root(1900) + "/update-start-blocked.json\n")
	for _, role := range []string{"app", "signer"} {
		stage := "/etc/systemd/system/fased-custody-1900-" + role + ".service.d/.20-fased-update-guard.conf-" + id + ".pending"
		args, err := BoundAtomicUnitWriteArgs(1900, stage, body)
		if err != nil || !AllowedBoundAtomicUnitWrite(1900, body, args) {
			t.Fatalf("no-clobber restart assertion refused: %v", err)
		}
		if !slices.Contains(args, "--no-clobber") || slices.Contains(args, "--property=StandardOutput=truncate:"+stage) {
			t.Fatal("restart assertion could overwrite an existing staging file")
		}
		if _, err = BoundAtomicUnitWriteArgs(1900, stage, append(bytes.Clone(body), 'x')); err == nil {
			t.Fatal("changed restart assertion accepted")
		}
		if _, err = BoundAtomicUnitWriteArgs(1901, stage, body); err == nil {
			t.Fatal("another owner's restart assertion accepted")
		}
	}
}

func TestBoundRestartAssertionOnlyAllowsExactOwnerAndContent(t *testing.T) {
	id := strings.Repeat("a", 32)
	body := []byte("[Unit]\nAssertPathExists=!/var/lib/fased-app-custody-1900/update-start-blocked.json\n")
	for _, role := range []string{"app", "signer"} {
		stage := "/etc/systemd/system/fased-custody-1900-" + role + ".service.d/.20-fased-update-guard.conf-" + id + ".pending"
		args, err := BoundUnitWriteArgs(1900, stage, body)
		if err != nil || !AllowedBoundUnitWrite(1900, body, args) {
			t.Fatal("exact owner assertion refused", role, err)
		}
		if _, err = BoundUnitWriteArgs(1900, stage, append(bytes.Clone(body), 'x')); err == nil {
			t.Fatal("changed assertion accepted", role)
		}
		if _, err = BoundUnitWriteArgs(1900, strings.Replace(stage, "1900", "1901", 1), body); err == nil {
			t.Fatal("foreign assertion destination accepted", role)
		}
	}
}

func TestRestartAssertionPublicationResumesAfterInterruption(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	body := []byte("[Unit]\nAssertPathExists=!" + Root(owner) + "/update-start-blocked.json\n")
	for _, role := range []string{"app", "signer"} {
		target := "/etc/systemd/system/" + Unit(owner, role) + ".d/20-fased-update-guard.conf"
		for failAfter := 1; failAfter <= 5; failAfter++ {
			h := &memoryHost{files: map[string]hostFile{}, failAfter: failAfter}
			_ = publish(t.Context(), target, id, body, 0644, h.read, h.run)
			h.failAfter = 0
			if err := publish(t.Context(), target, id, body, 0644, h.read, h.run); err != nil {
				t.Fatalf("%s interruption %d did not recover: %v", role, failAfter, err)
			}
			if f, err := h.read(target); err != nil || !known(f, body, 0644) {
				t.Fatal("exact root-owned assertion was not preserved", role, err)
			}
		}
	}
}
