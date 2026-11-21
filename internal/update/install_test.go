package update

import "testing"

func TestInstallBindingRejectsIncompleteRecovery(t *testing.T) {
	t.Parallel()
	if ValidateInstallBinding(InstallBinding{}) == nil {
		t.Fatal("incomplete installation binding accepted")
	}
}
