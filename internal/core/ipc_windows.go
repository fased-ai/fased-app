package core

import (
	"context"
	"errors"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenIPC(dir, id string) (net.Listener, string, string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", "", err
	}
	address := `\\.\pipe\fased-app-` + id
	listener, err := winio.ListenPipe(address, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")", MessageMode: false, InputBufferSize: 65536, OutputBufferSize: 65536})
	return listener, "pipe", address, err
}
func dialIPC(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "pipe" {
		return nil, errors.New("invalid local transport")
	}
	return winio.DialPipeContext(ctx, address)
}

func validateIPC(_ string, endpoint Endpoint) error {
	if endpoint.Network != "pipe" || endpoint.Address != `\\.\pipe\fased-app-`+endpoint.InstanceID {
		return errors.New("local pipe identity mismatch")
	}
	return nil
}
