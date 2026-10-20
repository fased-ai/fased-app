//go:build !linux

package signer

import (
	"context"
	"errors"
)

func decodeWire([]byte, any) error { return ErrUnavailable }
func VerifyExecutable() error      { return ErrUnavailable }
func LoadConfig(string) (Config, error) {
	return Config{}, errors.New("signer identity/IPC is unsupported on this OS")
}

type Server struct{}

func Serve(context.Context, Config) (*Server, error) { return nil, ErrUnavailable }
func (*Server) Close() error                         { return nil }
func call(context.Context, Config, bool, Request) (Response, error) {
	return Response{}, ErrUnavailable
}
