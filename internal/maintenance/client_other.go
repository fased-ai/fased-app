//go:build !linux

package maintenance

import (
	"context"
	"errors"
	"io"
)

var errUnsupported = errors.New("protected maintenance requires supported Linux")

func ReadAuthority(int) (Authority, error)                     { return Authority{}, errUnsupported }
func ValidateProcess(int) (Authority, error)                   { return Authority{}, errUnsupported }
func Call(context.Context, Request, io.Writer) ([]byte, error) { return nil, errUnsupported }
