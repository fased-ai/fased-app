package ownerclient

import (
	"errors"
)

// RemoteAdminConsent carries PUBLIC intent only. The console action downloads
// through the existing authenticated installer, never a browser-selected image.
// It reuses initial OS consent; ordinary SSH/recovery keys are not changed.
type RemoteAdminConsent struct{ Command string }

func (*RemoteAdminConsent) Error() string {
	return "Approve Wallets once in the server console, then reconnect."
}
func remoteAdminHandoff(c *Client) error {
	if c == nil || c.host == "" || !sshUser.MatchString(c.user) || c.Intent.Validate() != nil {
		return errors.New("invalid Remote Wallets target")
	}
	return &RemoteAdminConsent{Command: "bash <(curl -fsSL https://github.com/fased-ai/fased-app/releases/latest/download/install.sh) --remote --operator " + c.user + " --wallets --wallet-instance " + c.Intent.InstanceID + " --wallet-network " + c.Intent.Network}
}
