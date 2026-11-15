//go:build linux

package custodyinstall

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	var oversized bytes.Buffer
	large := tar.NewWriter(&oversized)
	if err := large.WriteHeader(&tar.Header{Name: "./usr/lib/fased-app/fased-app", Mode: 0755, Size: (64 << 20) + 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectDebTar(&oversized, false, testPackageFiles()); err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatal("oversized application executable was accepted", err)
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
		mode, flags := "100755", "0"
		if strings.Contains(name, "/doc/") {
			mode, hash, flags = "100644", strings.Repeat("e", 64), "2"
		}
		fmt.Fprintf(&listing, "/%s\t%s\troot\troot\t%s\t\t(none)\t%s\n", name, mode, hash, flags)
	}
	if err := validateRPMListing([]byte(listing.String()), "0.1.12", "x86_64", testPackageFiles()); err != nil {
		t.Fatal("matching RPM was refused", err)
	}
	for _, bad := range []string{
		strings.Replace(listing.String(), "../lib/fased-app/fased-app", "../../etc/shadow", 1),
		listing.String() + "/etc/sudoers.d/foreign\t100755\troot\troot\t" + strings.Repeat("e", 64) + "\t\t(none)\t0\n",
		strings.Replace(listing.String(), "(none)", "cap_net_admin=ep", 1),
		strings.Replace(listing.String(), "(none)\t0", "(none)\t64", 1),
		strings.Replace(listing.String(), "(none)\t2", "(none)\t66", 1),
		strings.Replace(listing.String(), "x86_64", "aarch64", 1),
	} {
		if err := validateRPMListing([]byte(bad), "0.1.12", "x86_64", testPackageFiles()); err == nil {
			t.Fatal("foreign RPM was accepted")
		}
	}
}

func TestNativeRPMInstallUsesAttestedPackageAndKeepsDigestChecks(t *testing.T) {
	path := "/verified/fased.rpm"
	name, args := nativePackageInstallCommand("dnf", path)
	if name != "/usr/bin/rpm" || !slices.Contains(args, "--nosignature") || args[len(args)-1] != path {
		t.Fatal("the verified RPM command differs", name, args)
	}
	for _, forbidden := range []string{"--nodigest", "--noverify", "--ignorepayload", "--ignoresize", "--nodeps"} {
		if slices.Contains(args, forbidden) {
			t.Fatal("RPM integrity or safety check was disabled", forbidden)
		}
	}
}

func TestDebianPredecessorMaintainerScriptsAreRefused(t *testing.T) {
	for _, name := range []string{
		"fased-app.prerm", "fased-app.postrm", "fased-app.preinst", "fased-app.postinst",
		"fased-app:amd64.prerm", "fased-app:arm64.postrm",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name), []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := verifyInstalledDebianControls(dir); err == nil {
				t.Fatal("installed package maintainer script was accepted")
			}
		})
	}
	dir := t.TempDir()
	for _, name := range []string{"fased-app.list", "fased-app.md5sums", "other-package.prerm", "fased-app-helper.postrm"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyInstalledDebianControls(dir); err != nil {
		t.Fatal("normal and unrelated package metadata must remain usable", err)
	}
	if err := verifyInstalledDebianControls(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("unavailable package metadata was accepted")
	}
}

func TestNativePredecessorInventoryPreservesForeignFiles(t *testing.T) {
	for _, family := range []string{"apt", "dnf"} {
		t.Run(family, func(t *testing.T) {
			paths := []string{"/usr/bin/fased", "/usr/lib/fased-app", "/usr/share/doc/fased-app"}
			for name := range testPackageFiles() {
				paths = append(paths, "/"+name)
			}
			if family == "apt" {
				paths = append(paths, "/.", "/usr", "/usr/bin", "/usr/lib", "/usr/share", "/usr/share/doc")
			}
			list := func(values []string) []byte { return []byte(strings.Join(values, "\n") + "\n") }
			if err := verifyInstalledPackagePaths(list(paths), family, testPackageFiles(), false); err != nil {
				t.Fatal("the package's own paths were refused", err)
			}
			for name, bad := range map[string][]string{
				"foreign data":    append(slices.Clone(paths), "/var/lib/fased/owner.db"),
				"unexpected path": append(slices.Clone(paths), "/etc/sudoers.d/fased"),
				"duplicate":       append(slices.Clone(paths), "/usr/bin/fased"),
				"unclean":         append(slices.Clone(paths), "/usr/lib/../local/foreign"),
			} {
				t.Run(name, func(t *testing.T) {
					if err := verifyInstalledPackagePaths(list(bad), family, testPackageFiles(), true); err == nil {
						t.Fatal("foreign package inventory was accepted")
					}
				})
			}
			missing := []string{"/usr/bin/fased"}
			if err := verifyInstalledPackagePaths(list(missing), family, testPackageFiles(), false); !errors.Is(err, errIncompleteNativePackageInventory) {
				t.Fatal("incomplete installed package inventory was not classified", err)
			}
			if err := verifyInstalledPackagePaths(list(missing), family, testPackageFiles(), true); err != nil {
				t.Fatal("a bounded interrupted inventory was refused", err)
			}
		})
	}
}

func retainedNativePackage(t *testing.T, packagePath string) (string, map[string]string) {
	t.Helper()
	var receipt struct {
		Product string `json:"product"`
		Inputs  map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"inputs"`
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(packagePath), "packages.json"))
	if err != nil || json.Unmarshal(raw, &receipt) != nil {
		t.Fatal("the retained package receipt is unavailable", err)
	}
	files := map[string]string{
		"usr/lib/fased-app/fased-app":     receipt.Inputs["fased-app"].SHA256,
		"usr/lib/fased-app/fased-install": receipt.Inputs["fased-install"].SHA256,
		"usr/lib/fased-app/fased-signer":  receipt.Inputs["fased-signer"].SHA256,
		"usr/share/doc/fased-app/LICENSE": "",
		"usr/share/doc/fased-app/NOTICE":  "",
	}
	for _, name := range []string{"fased-app", "fased-install", "fased-signer"} {
		if len(receipt.Inputs[name].SHA256) != 64 {
			t.Fatalf("the retained receipt is missing %s", name)
		}
	}
	return receipt.Product, files
}

func TestNativeRPMInspectionOfRetainedPackage(t *testing.T) {
	packagePath := os.Getenv("FASED_TEST_RPM")
	if packagePath == "" {
		t.Skip("retained RPM fixture not supplied")
	}
	version, files := retainedNativePackage(t, packagePath)
	_, arch, err := packageArch()
	if err != nil {
		t.Fatal(err)
	}
	listing, err := packageTool(t.Context(), 64<<10, "/usr/bin/rpm", "-qp", "--queryformat", rpmPackageListingQuery, "--", packagePath)
	if err != nil {
		t.Fatal("RPM metadata inspection failed", err)
	}
	if err = validateRPMListing(listing, version, arch, files); err != nil {
		t.Fatal("the retained Fased RPM was refused", err)
	}
}

func TestNativeDebInspectionOfRetainedPackage(t *testing.T) {
	packagePath, tool := os.Getenv("FASED_TEST_DEB"), os.Getenv("FASED_TEST_DPKG_DEB")
	if packagePath == "" || tool == "" {
		t.Skip("retained Debian package and inspection tool not supplied")
	}
	version, files := retainedNativePackage(t, packagePath)
	arch, _, err := packageArch()
	if err != nil {
		t.Fatal(err)
	}
	if err = inspectDebianPackageWithTool(t.Context(), tool, packagePath, version, arch, files); err != nil {
		t.Fatal("the retained Fased Debian package was refused", err)
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

func TestNativeBaseRecoveryRequiresOriginalCompletePackage(t *testing.T) {
	for _, test := range []struct {
		name, family, version, status string
		filesErr                      error
		wantOK, wantFileCheck         bool
	}{
		{"original RPM", "dnf", "0.1.10", "installed", nil, true, true},
		{"original DEB", "apt", "0.1.10", "install ok installed", nil, true, true},
		{"newer target", "dnf", "0.1.12", "installed", nil, false, false},
		{"interrupted DEB", "apt", "0.1.10", "install ok unpacked", nil, false, false},
		{"changed package files", "dnf", "0.1.10", "installed", fmt.Errorf("changed files"), false, true},
		{"unknown package family", "other", "0.1.10", "installed", nil, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			checked := false
			err := requireInstalledNativeBase(test.family, "0.1.10", test.version, test.status, func() error {
				checked = true
				return test.filesErr
			})
			if (err == nil) != test.wantOK || checked != test.wantFileCheck {
				t.Fatalf("original package decision: err=%v, files checked=%v", err, checked)
			}
		})
	}
}
