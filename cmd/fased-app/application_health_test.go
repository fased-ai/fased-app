package main

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"testing"
)

func TestApplicationHealthWaitsOnlyForUnavailableTransport(t *testing.T) {
	n := 0
	_, e := waitApplicationOwnerHealth(t.Context(), func(context.Context) (core.ApplicationOwnerView, error) {
		n++
		if n < 3 {
			return core.ApplicationOwnerView{}, core.ErrCoreUnavailable
		}
		return core.ApplicationOwnerView{}, nil
	})
	if e != nil || n != 3 {
		t.Fatal(n, e)
	}
	denied := errors.New("identity or authentication refused")
	n = 0
	_, e = waitApplicationOwnerHealth(t.Context(), func(context.Context) (core.ApplicationOwnerView, error) {
		n++
		return core.ApplicationOwnerView{}, denied
	})
	if e != denied || n != 1 {
		t.Fatal(n, e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e = waitApplicationOwnerHealth(ctx, func(context.Context) (core.ApplicationOwnerView, error) {
		return core.ApplicationOwnerView{}, core.ErrCoreUnavailable
	})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
