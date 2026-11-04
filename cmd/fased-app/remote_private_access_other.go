//go:build !linux

package main

import (
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/privateaccess"
)

// Remote installation is Linux-only; other clients have no OS elevation path.
func remotePrivateAccess(_ *custodyinstall.System) privateaccess.Manager {
	return privateaccess.System()
}
