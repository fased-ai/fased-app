//go:build linux

package custodyinstall

import "testing"

func TestProviderCredentialEncryptionIsExact(t *testing.T) {
	args := []string{"/usr/bin/systemd-creds", "encrypt", "--with-key=host", "--name=fased-provider-task-key", "-", "-"}
	if !providerCredentialEncryption(make([]byte, 32), args) {
		t.Fatal("fixed OS encryption rejected")
	}
	for i := range args {
		changed := append([]string(nil), args...)
		changed[i] += "-foreign"
		if providerCredentialEncryption(make([]byte, 32), changed) {
			t.Fatal("expanded OS encryption admitted", i)
		}
	}
	if providerCredentialEncryption(nil, args) || providerCredentialEncryption(make([]byte, 33), args) {
		t.Fatal("unbounded plaintext admitted")
	}
}
