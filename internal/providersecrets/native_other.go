//go:build !linux

package providersecrets

import "context"

func (Native) Key(context.Context, string, bool) ([]byte, error) { return nil, ErrUnavailable }
