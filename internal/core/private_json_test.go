package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type checkedJSON struct{ check func() error }

func (v checkedJSON) MarshalJSON() ([]byte, error) {
	if e := v.check(); e != nil {
		return nil, e
	}
	return []byte(`{"complete":true}`), nil
}
func TestPrivateJSONPublishesOnlyCompleteDocuments(t *testing.T) {
	path := filepath.Join(fixtureDir(t), "endpoint.json")
	if e := WritePrivateJSON(path, checkedJSON{func() error {
		if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
			return errors.New("incomplete destination was exposed before publication")
		}
		return nil
	}}); e != nil {
		t.Fatal(e)
	}
	before, e := ReadPrivateFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = WritePrivateJSON(path, checkedJSON{func() error {
		b, e := ReadPrivateFile(path)
		if e != nil {
			return e
		}
		if string(b) != string(before) {
			return errors.New("existing document changed before publication")
		}
		return nil
	}}); e != nil {
		t.Fatal(e)
	}
	invalid := filepath.Join(filepath.Dir(path), "invalid.json")
	if e = WritePrivateJSON(invalid, make(chan int)); e == nil {
		t.Fatal("marshal should fail")
	}
	if _, e = os.Lstat(invalid); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("failed write published a placeholder", e)
	}
}
