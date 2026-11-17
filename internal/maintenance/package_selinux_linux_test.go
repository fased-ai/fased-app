//go:build linux

package maintenance

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPackageSELinuxRulesAreLimitedToOneInstallation(t *testing.T) {
	a := Authority{Version: 1, Owner: 1000, Group: 1000, Home: "/home/owner", Profile: "local", ImageSHA256: strings.Repeat("a", 64)}
	id := strings.Repeat("b", 32)
	rules, err := packageSELinuxRules(a, "local", id)
	if err != nil || len(rules) != 3 {
		t.Fatalf("rules: %v, %v", rules, err)
	}
	for _, rule := range rules {
		re := regexp.MustCompile("^" + rule.pattern + "$")
		for _, example := range rule.examples {
			if !re.MatchString(example) {
				t.Fatalf("rule %q excludes its executable %q", rule.pattern, example)
			}
		}
		for _, foreign := range []string{
			"/var/lib/fased-app-package/1000/local/" + id + "/app-state/core.sqlite",
			"/var/lib/fased-app-package/1001/local/" + id + "/fased-app",
			"/var/lib/fased-app-package/1000/remote/" + id + "/fased-app",
			"/var/lib/fased-app-package/1000/local/" + strings.Repeat("c", 32) + "/fased-app",
			"/var/lib/fased-maintenance/instances/1000/local/" + id + "/updates/" + id + "/approval.json",
		} {
			if re.MatchString(foreign) {
				t.Fatalf("rule %q includes %q", rule.pattern, foreign)
			}
		}
	}
	for _, path := range []string{
		"/var/lib/fased-app-package/1000/local/" + id + "/fased-app",
		"/var/lib/fased-app-package/1000/local/." + id + ".pending/fased-signer",
		"/var/lib/fased-maintenance/instances/1000/local/" + id + "/provision/fased-app",
		"/var/lib/fased-maintenance/instances/1000/local/" + id + "/updates/" + id + "/fased-signer",
	} {
		if !packageExecutablePath(path) {
			t.Fatalf("fixed executable was missed: %s", path)
		}
	}
	if packageExecutablePath("/var/lib/fased-app-package/1000/local/" + id + "/signer-state/key") {
		t.Fatal("signer state must never be relabelled as executable")
	}
	if _, err = packageSELinuxRules(a, "remote", id); err == nil {
		t.Fatal("foreign profile accepted")
	}
}

// Exercise the distribution's real file-context matcher without changing the
// host policy. Rules must match both existing and future executable paths.
func TestPackageSELinuxRulesWithSystemMatcher(t *testing.T) {
	matcher, err := exec.LookPath("matchpathcon")
	if err != nil {
		t.Skip("SELinux file-context matcher is not installed")
	}
	a := Authority{Version: 1, Owner: 1000, Group: 1000, Home: "/home/owner", Profile: "local", ImageSHA256: strings.Repeat("a", 64)}
	id := strings.Repeat("b", 32)
	rules, err := packageSELinuxRules(a, "local", id)
	if err != nil {
		t.Fatal(err)
	}
	var policy strings.Builder
	policy.WriteString("/var/lib(/.*)? -- system_u:object_r:var_lib_t:s0\n")
	for _, rule := range rules {
		fmt.Fprintf(&policy, "%s -- system_u:object_r:bin_t:s0\n", rule.pattern)
	}
	file := filepath.Join(t.TempDir(), "file_contexts")
	if err = os.WriteFile(file, []byte(policy.String()), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, kind string }{
		{rules[0].examples[0], "bin_t"},
		{rules[1].examples[0], "bin_t"},
		{rules[2].examples[0], "bin_t"},
		{"/var/lib/fased-app-package/1000/local/" + id + "/signer-state/key", "var_lib_t"},
		{"/var/lib/fased-app-package/1000/local/" + id + "/fased-app/child", "var_lib_t"},
		{"/var/lib/fased-app-package/1001/local/" + id + "/fased-app", "var_lib_t"},
	} {
		out, e := exec.Command(matcher, "-f", file, "-n", "-m", "file", tc.path).CombinedOutput()
		if e != nil || !strings.Contains(string(out), ":"+tc.kind+":") {
			t.Fatalf("%s: expected %s, got %q (%v)", tc.path, tc.kind, out, e)
		}
	}
}

func TestPackageSELinuxRecoveryTargetsOnlyOriginalPair(t *testing.T) {
	id := strings.Repeat("b", 32)
	var paths []string
	failure := errors.New("relabel failed")
	apply := func(path string, mode os.FileMode) error {
		if mode != 0755 {
			t.Fatalf("unexpected executable mode %o", mode)
		}
		paths = append(paths, path)
		if strings.HasSuffix(path, "/fased-app") {
			return failure
		}
		return nil
	}
	if err := restorePackageServiceExecutables(1000, "local", id, apply); !errors.Is(err, failure) {
		t.Fatalf("failure must stop service recovery: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/var/lib/fased-app-package/1000/local/"+id+"/fased-install" || paths[1] != "/var/lib/fased-app-package/1000/local/"+id+"/fased-app" {
		t.Fatalf("recovery touched unexpected paths: %v", paths)
	}
	if err := restorePackageServiceExecutables(1000, "local", "../other", apply); err == nil {
		t.Fatal("foreign package identity accepted")
	}
}

func TestPackageSELinuxRegistrationFailsClosed(t *testing.T) {
	rules := []packageSELinuxRule{{pattern: "/fixed/fased-app", examples: []string{"/fixed/fased-app"}}}
	inspectionErr := errors.New("cannot inspect policy")
	added := 0
	if err := applyPackageSELinuxRules(rules, func(string) (bool, error) { return false, inspectionErr }, func(string) error {
		added++
		return nil
	}); !errors.Is(err, inspectionErr) || added != 0 {
		t.Fatalf("inspection failure changed policy: err=%v calls=%d", err, added)
	}
	rules[0].examples = append(rules[0].examples, "/fixed/fased-signer")
	if err := applyPackageSELinuxRules(rules, func(path string) (bool, error) {
		if strings.HasSuffix(path, "signer") {
			return false, inspectionErr
		}
		return false, nil
	}, func(string) error { added++; return nil }); !errors.Is(err, inspectionErr) || added != 0 {
		t.Fatalf("later policy conflict changed policy: err=%v calls=%d", err, added)
	}
	conflict := errors.New("existing rule differs")
	if err := applyPackageSELinuxRules(rules, func(string) (bool, error) { return false, nil }, func(string) error { return conflict }); !errors.Is(err, conflict) {
		t.Fatalf("conflicting policy was not preserved: %v", err)
	}
	installed := false
	if err := applyPackageSELinuxRules(rules, func(string) (bool, error) { return installed, nil }, func(pattern string) error {
		if pattern != rules[0].pattern {
			t.Fatalf("unexpected policy: %s", pattern)
		}
		installed = true
		return nil
	}); err != nil || !installed {
		t.Fatalf("exact rule was not registered: %v", err)
	}
}

func TestPackageSELinuxPolicyPreservesCustomTypes(t *testing.T) {
	for _, tc := range []struct {
		context string
		match   bool
		refuse  bool
	}{
		{"system_u:object_r:bin_t:s0", true, false},
		{"system_u:object_r:var_lib_t:s0", false, false},
		{"system_u:object_r:custom_exec_t:s0", false, true},
		{"invalid", false, true},
	} {
		got, err := packageSELinuxPolicyContext(tc.context)
		if got != tc.match || (err != nil) != tc.refuse {
			t.Fatalf("%q: match=%v error=%v", tc.context, got, err)
		}
	}
}
