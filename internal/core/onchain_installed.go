package core

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/signer"
)

// Trusted compiled fixture dependency only. No HTTP/settings path selects this.
func (server *Server) ConfigureControlledInstalledSwaps(path string, venue func(Account) (ControlledSwapVenue, error)) error {
	c, err := signer.LoadConfig(path)
	if err != nil {
		return err
	}
	if c.Version != 3 || c.Mode != signer.OrdinaryCustodyMode || c.Profile != signer.OwnerCustodyProfile || c.Provenance != signer.ControlledCustodyProfile {
		return errors.New("controlled ordinary installed identity required")
	}
	return server.Store.configureInstalledSwapFixture(path, venue)
}
