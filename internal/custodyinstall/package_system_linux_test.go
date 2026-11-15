//go:build linux

package custodyinstall

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func testPackageFiles() map[string]string {
	h := sha256.Sum256([]byte("verified executable"))
	digest := hex.EncodeToString(h[:])
	return map[string]string{
		"usr/lib/fased-app/fased-app": digest, "usr/lib/fased-app/fased-signer": digest,
		"usr/lib/fased-app/fased-install": digest, "usr/share/doc/fased-app/LICENSE": "", "usr/share/doc/fased-app/NOTICE": "",
	}
}

func debTestArchive(t *testing.T, extra string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	for name := range testPackageFiles() {
		mode := int64(0755)
		if strings.Contains(name, "/doc/") {
			mode = 0644
		}
		b := []byte("verified executable")
		if err := w.WriteHeader(&tar.Header{Name: "./" + name, Mode: mode, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.WriteHeader(&tar.Header{Name: "./usr/bin/fased", Mode: 0777, Typeflag: tar.TypeSymlink, Linkname: "../lib/fased-app/fased-app"}); err != nil {
		t.Fatal(err)
	}
	if extra != "" {
		if err := w.WriteHeader(&tar.Header{Name: extra, Mode: 0755, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestNativePackageInspectionRefusesForeignFilesAndScripts(t *testing.T) {
	data := debTestArchive(t, "")
	if _, err := inspectDebTar(bytes.NewReader(data), false, testPackageFiles()); err != nil {
		t.Fatal("matching package was refused", err)
	}
	wrong := testPackageFiles()
	wrong["usr/lib/fased-app/fased-signer"] = strings.Repeat("a", 64)
	if _, err := inspectDebTar(bytes.NewReader(data), false, wrong); err == nil {
		t.Fatal("different signer was accepted")
	}
	if _, err := inspectDebTar(bytes.NewReader(debTestArchive(t, "./etc/sudoers.d/foreign")), false, testPackageFiles()); err == nil {
		t.Fatal("package could install an unrelated privileged file")
	}
	var control bytes.Buffer
	w := tar.NewWriter(&control)
	_ = w.WriteHeader(&tar.Header{Name: "./postinst", Mode: 0755, Typeflag: tar.TypeReg})
	_ = w.Close()
	if _, err := inspectDebTar(&control, true, nil); err == nil {
		t.Fatal("package maintainer script was accepted")
	}
	controlWithRoot := func(mode int64) []byte {
		var archive bytes.Buffer
		w := tar.NewWriter(&archive)
		_ = w.WriteHeader(&tar.Header{Name: "./", Mode: mode, Typeflag: tar.TypeDir})
		_ = w.WriteHeader(&tar.Header{Name: "./control", Mode: 0644, Size: 1, Typeflag: tar.TypeReg})
		_, _ = w.Write([]byte("x"))
		_ = w.Close()
		return archive.Bytes()
	}
	if _, err := inspectDebTar(bytes.NewReader(controlWithRoot(0755)), true, nil); err != nil {
		t.Fatal("matching root directory was refused", err)
	}
	if _, err := inspectDebTar(bytes.NewReader(controlWithRoot(0700)), true, nil); err == nil {
		t.Fatal("package could change the root directory mode")
	}
}

func TestNativeRPMInspectionBindsTheApprovedPair(t *testing.T) {
	files := testPackageFiles()
	var listing strings.Builder
	listing.WriteString("fased-app\t0.1.12\t1\tx86_64\n")
	listing.WriteString("/usr/lib/fased-app\t40755\troot\troot\t\t\t(none)\t0\n")
	listing.WriteString("/usr/share/doc/fased-app\t40755\troot\troot\t\t\t(none)\t0\n")
	listing.WriteString("/usr/bin/fased\t120777\troot\troot\t\t../lib/fased-app/fased-app\t(none)\t0\n")
	for name, hash := range files {
		mode := "100755"
		if strings.Contains(name, "/doc/") {
			mode, hash = "100644", strings.Repeat("e", 64)
		}
		fmt.Fprintf(&listing, "/%s\t%s\troot\troot\t%s\t\t(none)\t0\n", name, mode, hash)
	}
	if err := validateRPMListing([]byte(listing.String()), "0.1.12", "x86_64", testPackageFiles()); err != nil {
		t.Fatal("matching RPM was refused", err)
	}
	for _, bad := range []string{
		strings.Replace(listing.String(), "../lib/fased-app/fased-app", "../../etc/shadow", 1),
		listing.String() + "/etc/sudoers.d/foreign\t100755\troot\troot\t" + strings.Repeat("e", 64) + "\t\t(none)\t0\n",
		strings.Replace(listing.String(), "(none)", "cap_net_admin=ep", 1),
		strings.Replace(listing.String(), "(none)\t0", "(none)\t64", 1),
		strings.Replace(listing.String(), "x86_64", "aarch64", 1),
	} {
		if err := validateRPMListing([]byte(bad), "0.1.12", "x86_64", testPackageFiles()); err == nil {
			t.Fatal("foreign RPM was accepted")
		}
	}
}

func TestNativePackageTrailerIsBoundedAndEmpty(t *testing.T) {
	if err := drainPackageTrailer(bytes.NewReader(make([]byte, 8192))); err != nil {
		t.Fatal(err)
	}
	if err := drainPackageTrailer(bytes.NewReader([]byte{0, 0, 1})); err == nil {
		t.Fatal("nonzero trailing package data was accepted")
	}
	if err := drainPackageTrailer(bytes.NewReader(make([]byte, (1<<20)+1))); err == nil {
		t.Fatal("unbounded package trailer was accepted")
	}
}
