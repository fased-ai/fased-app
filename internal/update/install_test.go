package update

import "testing"

func TestInstallBindingRejectsIncompleteRecovery(t *testing.T) {
	if ValidateInstallBinding(InstallBinding{}) == nil {
		t.Fatal("incomplete installation binding accepted")
	}
}
