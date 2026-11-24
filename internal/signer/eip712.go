package signer

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
)

// EIP712Domain defines the structured typed-data domain parameters.
type EIP712Domain struct {
	Name              string
	Version           string
	ChainID           *big.Int
	VerifyingContract string
	Salt              [32]byte
	HasSalt           bool
}

var eip712AgentTypeHash = Keccak256([]byte("Agent(string source,bytes32 connectionId)"))

// EIP712DomainSeparator computes hashStruct(eip712Domain) per EIP-712.
func EIP712DomainSeparator(d EIP712Domain) ([32]byte, error) {
	var fields []string
	var encoded [][]byte

	if d.Name != "" {
		fields = append(fields, "string name")
		h := Keccak256([]byte(d.Name))
		encoded = append(encoded, h[:])
	}
	if d.Version != "" {
		fields = append(fields, "string version")
		h := Keccak256([]byte(d.Version))
		encoded = append(encoded, h[:])
	}
	if d.ChainID != nil {
		if d.ChainID.Sign() < 0 || d.ChainID.BitLen() > 256 {
			return [32]byte{}, errors.New("invalid EIP-712 chainId")
		}
		fields = append(fields, "uint256 chainId")
		var word [32]byte
		d.ChainID.FillBytes(word[:])
		encoded = append(encoded, word[:])
	}
	if d.VerifyingContract != "" {
		canon, err := ValidateEthereumAddress(d.VerifyingContract)
		if err != nil {
			return [32]byte{}, err
		}
		raw, _ := hex.DecodeString(canon[2:])
		fields = append(fields, "address verifyingContract")
		var word [32]byte
		copy(word[12:], raw)
		encoded = append(encoded, word[:])
	}
	if d.HasSalt {
		fields = append(fields, "bytes32 salt")
		saltCopy := d.Salt
		encoded = append(encoded, saltCopy[:])
	}
	if len(fields) == 0 {
		return [32]byte{}, errors.New("empty EIP-712 domain")
	}

	typeString := "EIP712Domain(" + strings.Join(fields, ",") + ")"
	typeHash := Keccak256([]byte(typeString))

	var buf bytes.Buffer
	buf.Write(typeHash[:])
	for _, w := range encoded {
		buf.Write(w)
	}
	return Keccak256(buf.Bytes()), nil
}

// EIP712Digest computes keccak256("\x19\x01" || domainSeparator || structHash).
func EIP712Digest(domainSeparator, structHash [32]byte) [32]byte {
	return Keccak256([]byte{0x19, 0x01}, domainSeparator[:], structHash[:])
}

// HyperliquidExchangeDomain returns the canonical Hyperliquid L1 phantom-agent domain.
func HyperliquidExchangeDomain() EIP712Domain {
	return EIP712Domain{
		Name:              "Exchange",
		Version:           "1",
		ChainID:           big.NewInt(1337),
		VerifyingContract: "0x0000000000000000000000000000000000000000",
	}
}

type msgpackKV struct {
	Key string
	Val any
}

func appendMsgpackString(buf *bytes.Buffer, s string) error {
	b := []byte(s)
	n := len(b)
	switch {
	case n < 32:
		buf.WriteByte(byte(0xa0 | n))
	case n <= 0xff:
		buf.WriteByte(0xd9)
		buf.WriteByte(byte(n))
	case n <= 0xffff:
		buf.WriteByte(0xda)
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(n))
		buf.Write(l[:])
	default:
		return errors.New("msgpack string too large")
	}
	buf.Write(b)
	return nil
}

func appendMsgpackUint(buf *bytes.Buffer, v uint64) {
	switch {
	case v <= 0x7f:
		buf.WriteByte(byte(v))
	case v <= 0xff:
		buf.WriteByte(0xcc)
		buf.WriteByte(byte(v))
	case v <= 0xffff:
		buf.WriteByte(0xcd)
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(v))
		buf.Write(b[:])
	case v <= 0xffffffff:
		buf.WriteByte(0xce)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(v))
		buf.Write(b[:])
	default:
		buf.WriteByte(0xcf)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], v)
		buf.Write(b[:])
	}
}

func appendMsgpackValue(buf *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case nil:
		buf.WriteByte(0xc0)
	case bool:
		if val {
			buf.WriteByte(0xc3)
		} else {
			buf.WriteByte(0xc2)
		}
	case uint8:
		appendMsgpackUint(buf, uint64(val))
	case uint32:
		appendMsgpackUint(buf, uint64(val))
	case uint64:
		appendMsgpackUint(buf, val)
	case string:
		return appendMsgpackString(buf, val)
	case []msgpackKV:
		n := len(val)
		if n < 16 {
			buf.WriteByte(byte(0x80 | n))
		} else if n <= 0xffff {
			buf.WriteByte(0xde)
			var l [2]byte
			binary.BigEndian.PutUint16(l[:], uint16(n))
			buf.Write(l[:])
		} else {
			return errors.New("msgpack map too large")
		}
		for _, kv := range val {
			if err := appendMsgpackString(buf, kv.Key); err != nil {
				return err
			}
			if err := appendMsgpackValue(buf, kv.Val); err != nil {
				return err
			}
		}
	case []any:
		n := len(val)
		if n < 16 {
			buf.WriteByte(byte(0x90 | n))
		} else if n <= 0xffff {
			buf.WriteByte(0xdc)
			var l [2]byte
			binary.BigEndian.PutUint16(l[:], uint16(n))
			buf.Write(l[:])
		} else {
			return errors.New("msgpack array too large")
		}
		for _, item := range val {
			if err := appendMsgpackValue(buf, item); err != nil {
				return err
			}
		}
	default:
		return errors.New("unsupported msgpack field type")
	}
	return nil
}

func encodeHyperliquidMsgpack(kvs []msgpackKV) ([]byte, error) {
	var buf bytes.Buffer
	if err := appendMsgpackValue(&buf, kvs); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeHyperliquidOrderAction serializes a single limit order action in canonical
// Hyperliquid L1 msgpack key order.
func EncodeHyperliquidOrderAction(intent HyperliquidOrderIntent) ([]byte, error) {
	pxNorm, err := NormalizeHyperliquidDecimal(intent.LimitPrice)
	if err != nil {
		return nil, err
	}
	szNorm, err := NormalizeHyperliquidDecimal(intent.Size)
	if err != nil {
		return nil, err
	}
	if intent.TimeInForce != "Gtc" && intent.TimeInForce != "Ioc" && intent.TimeInForce != "Alo" {
		return nil, errors.New("unsupported timeInForce")
	}
	orderKV := []msgpackKV{
		{Key: "a", Val: intent.AssetIndex},
		{Key: "b", Val: intent.IsBuy},
		{Key: "p", Val: pxNorm},
		{Key: "s", Val: szNorm},
		{Key: "r", Val: intent.ReduceOnly},
		{Key: "t", Val: []msgpackKV{
			{Key: "limit", Val: []msgpackKV{
				{Key: "tif", Val: intent.TimeInForce},
			}},
		}},
	}
	if intent.ClientOrderID != "" {
		orderKV = append(orderKV, msgpackKV{Key: "c", Val: strings.ToLower(intent.ClientOrderID)})
	}
	grouping := intent.Grouping
	if grouping == "" {
		grouping = "na"
	}
	return encodeHyperliquidMsgpack([]msgpackKV{
		{Key: "type", Val: "order"},
		{Key: "orders", Val: []any{orderKV}},
		{Key: "grouping", Val: grouping},
	})
}

// EncodeHyperliquidUpdateLeverageAction serializes an updateLeverage L1 action
// in canonical Hyperliquid msgpack key order (type, asset, isCross, leverage).
func EncodeHyperliquidUpdateLeverageAction(intent HyperliquidUpdateLeverageIntent) ([]byte, error) {
	if intent.AssetIndex > 10000 || intent.Leverage == 0 {
		return nil, errors.New("updateLeverage requires valid asset index and non-zero leverage")
	}
	return encodeHyperliquidMsgpack([]msgpackKV{
		{Key: "type", Val: "updateLeverage"},
		{Key: "asset", Val: intent.AssetIndex},
		{Key: "isCross", Val: intent.IsCross},
		{Key: "leverage", Val: intent.Leverage},
	})
}

// EncodeHyperliquidCancelAction serializes a single cancel-by-oid action in canonical
// Hyperliquid L1 msgpack key order.
func EncodeHyperliquidCancelAction(intent HyperliquidCancelIntent) ([]byte, error) {
	if intent.OrderID == 0 {
		return nil, errors.New("cancel requires non-zero orderId")
	}
	cancelKV := []msgpackKV{
		{Key: "a", Val: intent.AssetIndex},
		{Key: "o", Val: intent.OrderID},
	}
	return encodeHyperliquidMsgpack([]msgpackKV{
		{Key: "type", Val: "cancel"},
		{Key: "cancels", Val: []any{cancelKV}},
	})
}

// HyperliquidActionConnectionID computes keccak256(msgpack(action) || uint64_be(nonce) || vaultFlag [|| vault20]).
func HyperliquidActionConnectionID(actionMsgpack []byte, nonce uint64, vaultAddress string) ([32]byte, error) {
	if len(actionMsgpack) == 0 || nonce == 0 {
		return [32]byte{}, errors.New("action msgpack and non-zero nonce required")
	}
	var nonceBE [8]byte
	binary.BigEndian.PutUint64(nonceBE[:], nonce)
	if vaultAddress == "" {
		return Keccak256(actionMsgpack, nonceBE[:], []byte{0x00}), nil
	}
	canonVault, err := ValidateEthereumAddress(vaultAddress)
	if err != nil {
		return [32]byte{}, err
	}
	vault20, _ := hex.DecodeString(canonVault[2:])
	return Keccak256(actionMsgpack, nonceBE[:], []byte{0x01}, vault20), nil
}

// HyperliquidAgentDigest computes the EIP-712 Exchange Agent typed-data digest for
// mainnet ("a") or testnet ("b").
func HyperliquidAgentDigest(network string, connectionID [32]byte) ([32]byte, error) {
	var source string
	switch network {
	case "mainnet":
		source = "a"
	case "testnet":
		source = "b"
	default:
		return [32]byte{}, errors.New("Hyperliquid network must be mainnet or testnet")
	}
	domainSep, err := EIP712DomainSeparator(HyperliquidExchangeDomain())
	if err != nil {
		return [32]byte{}, err
	}
	sourceHash := Keccak256([]byte(source))
	structHash := Keccak256(eip712AgentTypeHash[:], sourceHash[:], connectionID[:])
	return EIP712Digest(domainSep, structHash), nil
}
