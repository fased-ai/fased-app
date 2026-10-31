//go:build !linux

package core

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
)

func BootstrapApplicationReader(context.Context, string, ApplicationReaderBootstrapRequest) (ApplicationReaderBootstrapReceipt, error) {
	return ApplicationReaderBootstrapReceipt{}, errors.New("installed reader-bootstrap supports Linux only")
}

func ResumeApplicationReaderBootstrap(context.Context, string, string) (ApplicationReaderBootstrapReceipt, error) {
	return ApplicationReaderBootstrapReceipt{}, errors.New("installed reader-bootstrap supports Linux only")
}

func StageApplicationReaderBootstrap(context.Context, string, ApplicationReaderBootstrapRequest) error {
	return errors.New("installed reader-bootstrap supports Linux only")
}
func ReadApplicationReaderBootstrapRequest(string, string) (ApplicationReaderBootstrapRequest, error) {
	return ApplicationReaderBootstrapRequest{}, errors.New("installed reader-bootstrap supports Linux only")
}
func CheckApplicationReaderPredecessor(context.Context, string, update.CoreSnapshot) error {
	return errors.New("installed reader-bootstrap supports Linux only")
}

func PrepareApplicationReaderBootstrap(context.Context, string, ApplicationReaderBootstrapRequest) error {
	return errors.New("installed reader-bootstrap supports Linux only")
}
