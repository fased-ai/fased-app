//go:build linux

package custodyinstall

import "testing"

func TestAllowedFindSSHConfigDir(t *testing.T) {
	t.Parallel()

	valid := []string{
		"/usr/bin/find",
		"/etc/ssh/sshd_config.d",
		"-mindepth", "1",
		"-maxdepth", "1",
		"-printf", "%U:%G:%m:%y:%s:%f\\0",
	}
	if !allowedFindSSHConfigDir(valid) {
		t.Fatal("exact read-only /etc/ssh/sshd_config.d find invocation rejected")
	}

	rejected := [][]string{
		{"/usr/bin/find", "/etc/ssh", "-mindepth", "1", "-maxdepth", "1", "-printf", "%U:%G:%m:%y:%s:%f\\0"},
		{"/usr/bin/find", "/etc/ssh/sshd_config.d/..", "-mindepth", "1", "-maxdepth", "1", "-printf", "%U:%G:%m:%y:%s:%f\\0"},
		{"/usr/bin/find", "/etc/ssh/sshd_config.d", "-mindepth", "1", "-maxdepth", "2", "-printf", "%U:%G:%m:%y:%s:%f\\0"},
		{"/usr/bin/find", "/etc/ssh/sshd_config.d", "-mindepth", "1", "-maxdepth", "1", "-printf", "%U:%G:%m:%y:%s:%f\\n"},
		{"/usr/bin/find", "/etc/ssh/sshd_config.d", "-mindepth", "1", "-maxdepth", "1", "-delete"},
		{"/usr/bin/find", "/etc/ssh/sshd_config.d", "-L", "-mindepth", "1", "-maxdepth", "1", "-printf", "%U:%G:%m:%y:%s:%f\\0"},
		{"/usr/bin/find", "/etc/ssh/sshd_config.d", "-mindepth", "1", "-maxdepth", "1", "-exec", "/bin/true", ";"},
		{"/usr/bin/find", "/etc/ssh/sshd_config.d", "-mindepth", "1", "-maxdepth", "1"},
	}
	for _, args := range rejected {
		if allowedFindSSHConfigDir(args) {
			t.Fatalf("unsafe find invocation accepted: %v", args)
		}
	}
}
