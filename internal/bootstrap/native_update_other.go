//go:build !linux

package bootstrap

import (
	"context"
	"errors"
)

func (p *Prepared) nativeUpdateFiles(context.Context, string) (nativeUpdateArtifacts, func() error, error) {
	return nativeUpdateArtifacts{}, nil, errors.New("authenticated native updates require Linux")
}
