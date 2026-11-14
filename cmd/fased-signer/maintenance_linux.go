//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"strconv"
)

func signerMaintenanceCommand(args []string, in io.Reader, out io.Writer) (result error) {
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(io.Discard)
	owner := f.Int("owner", 0, "")
	id := f.String("operation", "", "")
	probe := f.String("probe", "", "")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 || *owner <= 0 {
		return errors.New("fixed maintenance owner required")
	}
	c, err := signer.MaintenanceConfig(*owner)
	if err != nil {
		return err
	}
	if c.OwnerUID != *owner {
		return signer.ErrUnavailable
	}
	if args[0] == "custody-maintenance-status" {
		if !update.IDOK(*id) || *probe != "" {
			return signer.ErrUnavailable
		}
		a, err := update.ReadMaintenanceApproval(*owner, *id)
		if err != nil || a.InstallationID != c.SignerID {
			return errors.New("protected installation approval required")
		}
		v, err := signer.InspectInstallationReadOnly(c, *id)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(signer.Response{Installation: v})
	}
	if args[0] == "custody-maintenance-inventory" {
		if *id != "" || !update.IDOK(*probe) {
			return signer.ErrUnavailable
		}
		r, err := signer.InspectMaintenanceInventory(c, *probe)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	}
	if !update.IDOK(*id) || *probe != "" || strconv.Itoa(c.OwnerUID) != strconv.Itoa(*owner) {
		return signer.ErrUnavailable
	}
	s, err := signer.OpenMaintenance(c, *id)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, s.Close()) }()
	r := bufio.NewReaderSize(in, 64<<10)
	for count := 0; count < 24; count++ {
		b, err := r.ReadSlice('\n')
		if err == io.EOF && len(b) == 0 {
			return nil
		}
		if err != nil {
			return errors.New("bounded maintenance command required")
		}
		var q update.MaintenanceCommand
		if update.StrictJSON(b, &q) != nil || q.OperationID != *id {
			return errors.New("invalid maintenance command")
		}
		reply, err := s.Request(q)
		if err != nil {
			reply.Error = err.Error()
		}
		if e := json.NewEncoder(out).Encode(reply); e != nil {
			return e
		}
		if err != nil {
			return err
		}
	}
	return errors.New("maintenance command bound exhausted")
}
