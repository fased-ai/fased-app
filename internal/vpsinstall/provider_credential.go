package vpsinstall

import (
	"fmt"
	"github.com/fased-ai/fased-app/internal/providersecrets"
)

func taskCredentialID(m Manifest) string   { return fmt.Sprintf("fased-provider-task-%d", m.OwnerUID) }
func taskCredentialPath(m Manifest) string { return "/etc/credstore.encrypted/" + taskCredentialID(m) }
func taskCredentialDropInPath(m Manifest) string {
	return "/etc/systemd/system/" + m.Unit + ".d/30-fased-provider-credentials.conf"
}
func taskCredentialDropIn(m Manifest) []byte {
	return []byte("[Service]\nLoadCredentialEncrypted=" + providersecrets.CredentialName + ":" + taskCredentialPath(m) + "\n")
}
