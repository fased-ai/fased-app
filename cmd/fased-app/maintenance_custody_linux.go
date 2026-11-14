//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
)

func metadataMaintenanceCommand(args []string, in io.Reader, out io.Writer) (result error) {
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(io.Discard)
	owner := f.Int("owner", 0, "")
	id := f.String("operation", "", "")
	probe := f.String("probe", "", "")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 || *owner <= 0 {
		return errors.New("fixed maintenance owner required")
	}
	root := fmt.Sprintf("/var/lib/fased-app-custody-%d", *owner)
	c, err := signer.LoadConfig(root + "/identity.json")
	if err != nil {
		return err
	}
	if c.OwnerUID != *owner || os.Getuid() != c.AppUID || os.Geteuid() != c.AppUID {
		return errors.New("original app identity required")
	}
	if err = signer.CheckApplication(root+"/identity.json", root, root+"/app-state"); err != nil {
		return err
	}
	ctx := context.Background()
	if args[0] == "custody-maintenance-verify" {
		if !update.IDOK(*id) || *probe != "" {
			return errors.New("one exact original recovery required")
		}
		r, err := update.ReadMaintenanceRecord(*owner)
		if err != nil || r.OperationID != *id || r.Recovery == nil {
			return errors.New("protected original recovery index required")
		}
		if err = core.VerifyFixtureColdRecovery(ctx, root+"/app-state", *r.Recovery); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(update.MetadataMaintenanceReply{Recovery: r.Recovery})
	}
	if args[0] == "custody-maintenance-inventory" {
		if *id != "" || !update.IDOK(*probe) {
			return errors.New("one metadata probe required")
		}
		v, err := core.InspectMaintenanceMetadata(ctx, root+"/app-state", *probe)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(update.MetadataMaintenanceReply{Snapshot: &v})
	}
	if !update.IDOK(*id) || *probe != "" {
		return errors.New("one maintenance operation required")
	}
	s, err := core.OpenMetadataMaintenance(ctx, root+"/app-state", *owner, *id)
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
		reply, err := s.Request(ctx, q)
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
