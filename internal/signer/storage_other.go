//go:build !linux

package signer

import "errors"

var errAbsent = errors.New("absent")

type storage struct{}

func openStorage(string) (*storage, error) {
	return nil, errors.New("signer identity and protected transport are not implemented on this OS; wallets remain unavailable")
}
func (*storage) read() ([]byte, error) { return nil, ErrUnavailable }
func (*storage) create([]byte) error   { return ErrUnavailable }
func (*storage) close() error          { return nil }

func (*storage) readJournal(string) (*journal, []journalRecord, error) {
	return nil, nil, ErrUnavailable
}
func (*storage) appendJournal(*journal, journalRecord) error { return ErrUnavailable }
func (*storage) failedState() bool                           { return true }
func (*storage) poison()                                     {}
