package connection

import (
	"errors"
	"os/exec"
	"regexp"
	"runtime"
)

func OpenBrowser(value string, expected ...string) error {
	origin, err := Origin(value)
	if err != nil {
		return err
	}
	if len(expected) > 1 || (len(expected) == 1 && !identity.MatchString(expected[0])) {
		return errors.New("invalid expected instance identity")
	}
	if len(expected) == 1 {
		origin += "/?instance=" + expected[0]
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("/usr/bin/open", origin)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", origin)
	default:
		command = exec.Command("xdg-open", origin)
	}
	return command.Run()
}

// OwnerSetupURL is used only by the OS-owner launcher, never by Connect or an
// unauthenticated HTTP handler. The fragment is not sent to the HTTP server.
func OwnerSetupURL(value, instanceID, proof string) (string, error) {
	origin, err := Origin(value)
	if err != nil {
		return "", err
	}
	if !identity.MatchString(instanceID) || proof != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(proof) {
		return "", errors.New("invalid owner setup handoff")
	}
	address := origin + "/?instance=" + instanceID
	if proof != "" {
		address += "#setup=" + proof
	}
	return address, nil
}
