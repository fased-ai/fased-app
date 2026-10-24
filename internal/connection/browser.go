package connection

import (
	"errors"
	"os/exec"
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
