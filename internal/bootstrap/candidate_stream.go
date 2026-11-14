package bootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/update"
)

// WriteCandidate streams only the authenticated inventory already held by this
// preparation. The receiver independently verifies the packet and all bytes.
func (p *Prepared) WriteCandidate(ctx context.Context, out io.Writer) error {
	if p == nil || out == nil {
		return errors.New("held candidate and output required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.root == nil || p.candidate == nil {
		return errors.New("held candidate required")
	}
	if err := p.revalidate(); err != nil {
		return err
	}
	var packet AttestedPacket
	if update.StrictJSON(p.metadata[AttestedPacketFile], &packet) != nil {
		return errors.New("held candidate packet invalid")
	}
	tw := tar.NewWriter(out)
	write := func(name string, size int64, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		n, err := io.CopyBuffer(tw, io.LimitReader(r, size+1), make([]byte, 32<<10))
		if err != nil || n != size {
			return errors.New("candidate stream source changed")
		}
		return nil
	}
	for _, item := range []struct {
		name string
		data []byte
	}{{AttestedPacketFile, p.metadata[AttestedPacketFile]}, {provenanceFile, p.metadata["provenance.sigstore.json"]}} {
		if err := write(item.name, int64(len(item.data)), bytes.NewReader(item.data)); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		name, held string
		size       int64
	}{{packet.Archive.File, "download.tar.gz", packet.Archive.Size}, {packet.Installer.File, "candidate-installer", packet.Installer.Size}, {packet.Entry.File, "candidate-entry", packet.Entry.Size}} {
		f, err := p.root.Open(item.held)
		if err != nil {
			return err
		}
		info, statErr := f.Stat()
		entry, entryErr := p.root.Lstat(item.held)
		if statErr != nil || entryErr != nil || !info.Mode().IsRegular() || !os.SameFile(info, entry) || info.Size() != item.size {
			f.Close()
			return errors.New("candidate stream input changed")
		}
		err = errors.Join(write(item.name, item.size, f), f.Close())
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return errors.Join(ctx.Err(), p.revalidate())
}

// ReceiveCandidate accepts only the five fixed files in an empty private
// directory. It authenticates their packet-bound sizes and digests; the caller
// must still verify the original provenance with the independently pinned seed.
func ReceiveCandidate(ctx context.Context, in io.Reader, directory, packetSHA256 string) error {
	if in == nil || !hashOK(packetSHA256) {
		return errors.New("one bounded candidate stream required")
	}
	if _, err := privateDirectory(directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(1)
	err = errors.Join(readErr, dir.Close())
	if err != nil && !errors.Is(err, io.EOF) || len(entries) != 0 {
		return errors.New("candidate destination is not empty; preserve it")
	}
	tr := tar.NewReader(io.LimitReader(in, 2*maxArchiveBytes+4*int64(update.MaxDocument)+2<<20))
	header, err := tr.Next()
	if err != nil || header.Name != AttestedPacketFile || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > update.MaxDocument {
		return errors.New("candidate packet must be first and bounded")
	}
	raw, err := io.ReadAll(io.LimitReader(tr, int64(update.MaxDocument)+1))
	if err != nil || int64(len(raw)) != header.Size || digest(raw) != packetSHA256 {
		return errors.New("candidate stream packet differs from the reviewed identity")
	}
	var packet AttestedPacket
	if update.StrictJSON(raw, &packet) != nil || packet.Version != 2 || !attestedBuildIdentity(packet, "") || packet.Archive.File != archiveTarget || packet.Installer.File != "fased-install-linux-amd64" || packet.Entry.File != "install.sh" || packet.Release.Provenance.File != provenanceFile {
		return errors.New("candidate stream inventory invalid")
	}
	if err = writeNew(root, AttestedPacketFile, raw); err != nil {
		return err
	}
	for _, artifact := range []update.Artifact{packet.Release.Provenance, packet.Archive, packet.Installer, packet.Entry} {
		if err = ctx.Err(); err != nil {
			return err
		}
		limit := int64(maxArchiveBytes)
		if artifact.File == provenanceFile {
			limit = update.MaxDocument
		}
		if artifact.Size <= 0 || artifact.Size > limit || !hashOK(artifact.SHA256) {
			return errors.New("candidate stream artifact bounds invalid")
		}
		header, err = tr.Next()
		if err != nil || header.Typeflag != tar.TypeReg || header.Name != artifact.File || header.Size != artifact.Size {
			return errors.New("candidate stream inventory differs")
		}
		f, err := root.OpenFile(artifact.File, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		h := sha256.New()
		if err = f.Chmod(0600); err == nil {
			var n int64
			n, err = io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(tr, artifact.Size+1), make([]byte, 32<<10))
			if err == nil && (n != artifact.Size || hex.EncodeToString(h.Sum(nil)) != artifact.SHA256) {
				err = errors.New("candidate stream artifact differs")
			}
		}
		err = errors.Join(err, f.Sync(), f.Close())
		if err != nil {
			return err
		}
	}
	if _, err = tr.Next(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected candidate stream entries")
	}
	return ctx.Err()
}
