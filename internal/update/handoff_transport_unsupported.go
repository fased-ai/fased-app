//go:build !linux

package update

import (
	"context"
	"errors"
	"net"
)

func fixtureTransportSupported() error                { return errors.New("fixture kernel transport is Linux-only") }
func fixtureRoleIdentity(*FixtureTransportRole) error { return fixtureTransportSupported() }
func fixturePeerUID(*net.UnixConn) (int, error)       { return -1, fixtureTransportSupported() }
func fixtureListen(*FixtureTransportRole) (*net.UnixListener, error) {
	return nil, fixtureTransportSupported()
}
func fixtureDial(context.Context, *FixtureTransportRole) (*net.UnixConn, error) {
	return nil, fixtureTransportSupported()
}
