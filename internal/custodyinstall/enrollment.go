package custodyinstall

import (
	"context"
	"io"
	"sync"
)

// EnrollmentCommit grants no wallet operation. It binds the complete app image
// committed before either managed service can be started.
type EnrollmentCommit struct {
	Version        int    `json:"version"`
	ManifestSHA256 string `json:"manifestSha256"`
	InstanceID     string `json:"instanceId"`
	Digest         string `json:"digest"`
}

type EnrollmentProcess struct {
	Input  io.WriteCloser
	Output io.ReadCloser
	cancel context.CancelFunc
	wait   func() error
	once   sync.Once
	err    error
}

func (p *EnrollmentProcess) Wait() error {
	p.once.Do(func() { p.err = p.wait() })
	return p.err
}

func (p *EnrollmentProcess) Close() error {
	_ = p.Input.Close()
	p.cancel()
	_ = p.Output.Close()
	return p.Wait()
}

// EnrollmentIdentity survives an independently verified managed executable update.
// Only the replaceable code pair is excluded; custody and instance topology stay bound.
func EnrollmentIdentity(m Manifest) Manifest {
	m.CoreHash, m.SignerHash = "", ""
	return m
}
