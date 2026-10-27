package wenpm1

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

const (
	Version        = 1
	FixtureOnly    = true
	MaxFrame       = 131072
	MaxInstruction = 256
)

var ErrInvalid = errors.New("invalid or unsupported fixture PM1 v1 data")

type writer struct {
	data []byte
	err  error
}

func (w *writer) put(b []byte) {
	if w.err != nil {
		return
	}
	if len(b) > MaxFrame-len(w.data) {
		w.err = ErrInvalid
		return
	}
	w.data = append(w.data, b...)
}
func (w *writer) number(n uint64, size int) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	w.put(b[:size])
}

type reader struct {
	data []byte
	at   int
	err  error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.data)-r.at {
		r.err = ErrInvalid
		return nil
	}
	b := r.data[r.at : r.at+n]
	r.at += n
	return b
}
func (r *reader) number(size int) uint64 {
	var b [8]byte
	copy(b[:], r.take(size))
	return binary.LittleEndian.Uint64(b[:])
}
func (r *reader) done() {
	if r.at != len(r.data) {
		r.err = ErrInvalid
	}
}
func collectionMax(f reflect.StructField) int {
	n, _ := strconv.Atoi(f.Tag.Get("wiremax"))
	return n
}

var keyType = reflect.TypeFor[Key]()
var annualType = reflect.TypeFor[Annual]()

// Only our sealed wire models enter this codec. Field order and collection
// bounds are checked against every pinned schema field by TestSchemaLayout.
func (w *writer) value(v reflect.Value, max int) {
	if w.err != nil {
		return
	}
	if v.Type() == keyType {
		k := v.Interface().(Key)
		w.put(k[:])
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			w.value(v.Field(i), collectionMax(v.Type().Field(i)))
		}
	case reflect.Uint8:
		w.number(v.Uint(), 1)
	case reflect.Uint32:
		w.number(v.Uint(), 4)
	case reflect.Uint64:
		w.number(v.Uint(), 8)
	case reflect.Int64:
		w.number(uint64(v.Int()), 8)
	case reflect.Bool:
		n := uint64(0)
		if v.Bool() {
			n = 1
		}
		w.number(n, 1)
	case reflect.Pointer:
		if v.IsNil() {
			w.number(0, 1)
		} else {
			w.number(1, 1)
			w.value(v.Elem(), 0)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			w.value(v.Index(i), 0)
		}
	case reflect.Slice:
		if max <= 0 || v.Len() > max {
			w.err = ErrInvalid
			return
		}
		w.number(uint64(v.Len()), 4)
		var prior uint64
		for i := 0; i < v.Len(); i++ {
			if v.Type() == annualType {
				k := v.Index(i).Field(0).Uint()
				if i > 0 && k <= prior {
					w.err = ErrInvalid
					return
				}
				prior = k
			}
			w.value(v.Index(i), 0)
		}
	default:
		w.err = ErrInvalid
	}
}
func (r *reader) value(v reflect.Value, max int) {
	if r.err != nil {
		return
	}
	if v.Type() == keyType {
		var k Key
		copy(k[:], r.take(32))
		v.Set(reflect.ValueOf(k))
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			r.value(v.Field(i), collectionMax(v.Type().Field(i)))
		}
	case reflect.Uint8:
		v.SetUint(r.number(1))
	case reflect.Uint32:
		v.SetUint(r.number(4))
	case reflect.Uint64:
		v.SetUint(r.number(8))
	case reflect.Int64:
		v.SetInt(int64(r.number(8)))
	case reflect.Bool:
		n := r.number(1)
		if n > 1 {
			r.err = ErrInvalid
		}
		v.SetBool(n == 1)
	case reflect.Pointer:
		n := r.number(1)
		if n > 1 {
			r.err = ErrInvalid
			return
		}
		if n == 1 {
			v.Set(reflect.New(v.Type().Elem()))
			r.value(v.Elem(), 0)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			r.value(v.Index(i), 0)
		}
	case reflect.Slice:
		n := r.number(4)
		if max <= 0 || n > uint64(max) {
			r.err = ErrInvalid
			return
		}
		v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		var prior uint64
		for i := uint64(0); i < n && r.err == nil; i++ {
			e := reflect.New(v.Type().Elem()).Elem()
			r.value(e, 0)
			if v.Type() == annualType {
				k := e.Field(0).Uint()
				if i > 0 && k <= prior {
					r.err = ErrInvalid
				}
				prior = k
			}
			if r.err == nil {
				v.Set(reflect.Append(v, e))
			}
		}
	default:
		r.err = ErrInvalid
	}
}
func (w *writer) header(magic string, kind byte) {
	w.put([]byte(magic))
	w.put([]byte{Version, 0, 1, kind})
}
func (r *reader) header(magic string, kind byte) {
	if !bytes.Equal(r.take(8), []byte(magic)) || !bytes.Equal(r.take(4), []byte{Version, 0, 1, kind}) {
		r.err = ErrInvalid
	}
}
func (b Binding) validate() error {
	for _, k := range []Key{b.Program, b.Ledger, b.Network, b.Economy, b.Terms} {
		if k == (Key{}) {
			return fmt.Errorf("%w: missing identity", ErrInvalid)
		}
	}
	return nil
}
func (b Binding) match(expected Binding) error {
	if err := expected.validate(); err != nil {
		return err
	}
	if err := b.validate(); err != nil {
		return err
	}
	if b != expected {
		return fmt.Errorf("%w: identity mismatch", ErrInvalid)
	}
	return nil
}
func encodeFrame(value any, b Binding, magic string, kind byte) ([]byte, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	body := writer{}
	body.value(reflect.ValueOf(value), 0)
	if body.err != nil {
		return nil, body.err
	}
	w := writer{}
	w.header(magic, kind)
	w.number(uint64(len(body.data)), 4)
	w.put(body.data)
	if w.err != nil {
		return nil, w.err
	}
	return w.data, nil
}
func decodeFrame(data []byte, expected Binding, value any, magic string, kind byte) error {
	if err := expected.validate(); err != nil {
		return err
	}
	if len(data) > MaxFrame {
		return ErrInvalid
	}
	r := reader{data: data}
	r.header(magic, kind)
	n := r.number(4)
	if n > MaxFrame {
		return ErrInvalid
	}
	body := reader{data: r.take(int(n))}
	if r.err != nil {
		return r.err
	}
	if kind == 1 {
		for _, b := range r.data[r.at:] {
			if b != 0 {
				return ErrInvalid
			}
		}
	} else {
		r.done()
	}
	if r.err != nil {
		return r.err
	}
	body.value(reflect.ValueOf(value).Elem(), 0)
	body.done()
	return body.err
}

func EncodeAccount(v Ledger) ([]byte, error) { return encodeFrame(v, v.Binding, "WENPM1AC", 1) }
func DecodeAccount(data []byte, expected Binding) (Ledger, error) {
	var v Ledger
	err := decodeFrame(data, expected, &v, "WENPM1AC", 1)
	if err == nil {
		err = v.Binding.match(expected)
	}
	if err != nil {
		return Ledger{}, err
	}
	return v, nil
}

// Receipts are unsigned projections, not proof of execution or payment.
func EncodeReceipt(v Receipt) ([]byte, error) { return encodeFrame(v, v.Binding, "WENPM1RC", 2) }
func DecodeReceipt(data []byte, expected Binding) (Receipt, error) {
	var v Receipt
	err := decodeFrame(data, expected, &v, "WENPM1RC", 2)
	if err == nil {
		err = v.Binding.match(expected)
	}
	if err != nil {
		return Receipt{}, err
	}
	return v, nil
}
