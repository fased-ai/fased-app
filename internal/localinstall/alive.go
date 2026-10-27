package localinstall

import (
	"context"
	"github.com/fased-ai/fased-app/internal/core"
)

func coreAlive(ctx context.Context, dir string) bool {
	c, e := core.NewClient(ctx, dir)
	if e != nil {
		return false
	}
	c.Close()
	return true
}
