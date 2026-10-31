package vpsinstall

import (
	"context"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

// Authenticated release data, never arbitrary JSON/IPC source/path authority.
type ApplicationUpdate = localinstall.ApplicationUpdate
type ApplicationUpdateStatus = localinstall.ApplicationUpdateStatus
type ApplicationUpdateCallbacks struct {
	BeforeStage     func(context.Context) error // Optional exact owner dispatch recheck under installer lock.
	BeforeStop      func(context.Context, Manifest, Manifest) error
	AfterStop       func(context.Context, Manifest, Manifest) error // Optional stopped reader-bootstrap; never an OS grant.
	Health          func(context.Context, Manifest) error
	RollbackAllowed func(context.Context, Manifest, Manifest) error
	Complete        func(context.Context, Manifest, ApplicationUpdateStatus) error
}

func ManifestSHA256(m Manifest) string { return hash(encodedApplicationManifest(m)) }
