package cex

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// SignBinanceHMAC computes the lowercase hex HMAC-SHA256 query signature expected by Binance APIs.
func SignBinanceHMAC(secret, queryString string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(queryString))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignBybitV5 computes the Bybit V5 lowercase hex HMAC-SHA256 header signature over
// timestamp + apiKey + recvWindow + payload.
func SignBybitV5(secret, timestampMs, apiKey, recvWindow, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestampMs + apiKey + recvWindow + payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignOKXV5 computes the base64 HMAC-SHA256 signature over
// timestamp + UPPER(method) + requestPath + body.
func SignOKXV5(secret, timestampISO, method, requestPath, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestampISO + strings.ToUpper(method) + requestPath + body))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// SignKraken computes Kraken's API-Sign header:
// Base64(HMAC-SHA512(urlPath + SHA256(nonce + encodedBody), Base64Decode(secret))).
func SignKraken(secretB64, urlPath, nonce, encodedBody string) (string, error) {
	decodedSecret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(secretB64))
	if err != nil || len(decodedSecret) == 0 {
		return "", errors.New("invalid base64 kraken secret")
	}
	sha := sha256.Sum256([]byte(nonce + encodedBody))
	msg := make([]byte, 0, len(urlPath)+len(sha))
	msg = append(msg, []byte(urlPath)...)
	msg = append(msg, sha[:]...)
	mac := hmac.New(sha512.New, decodedSecret)
	_, _ = mac.Write(msg)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// SignCoinbaseHMAC computes lowercase hex HMAC-SHA256 over timestamp + method + path + body.
func SignCoinbaseHMAC(secret, timestampSec, method, requestPath, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestampSec + strings.ToUpper(method) + requestPath + body))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignDeribitEd25519 signs a Deribit request payload using a 32-byte Ed25519 seed (raw/hex/base64).
func SignDeribitEd25519(secretSeed string, timestampMs, nonce, method, uri, body string) (string, error) {
	seed, err := parseEd25519Seed(secretSeed)
	if err != nil {
		return "", err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n", timestampMs, nonce, strings.ToUpper(method), uri, body)
	sig := ed25519.Sign(priv, []byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

func parseEd25519Seed(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if b, err := hex.DecodeString(strings.TrimPrefix(trimmed, "0x")); err == nil && len(b) == ed25519.SeedSize {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(trimmed); err == nil && len(b) == ed25519.SeedSize {
			return b, nil
		}
	}
	return nil, errors.New("expected 32-byte hex or base64 Ed25519 seed")
}

// SignOrderDigest builds a deterministic, non-secret SHA-256 audit digest of the
// venue-canonical signed request envelope without embedding raw secret material.
func SignOrderDigest(venue, apiKey, secret, tsMs string, req OrderRequest) (string, error) {
	if secret == "" {
		// Paper/uncredentialed draft digest over public order fields only.
		canonical := fmt.Sprintf("paper|%s|%s|%s|%s|%.8f|%.8f|%s", req.Venue, req.Symbol, req.Side, req.OrderType, req.Quantity, req.Price, tsMs)
		sum := sha256.Sum256([]byte(canonical))
		return "0x" + hex.EncodeToString(sum[:]), nil
	}
	var rawSig string
	payload := fmt.Sprintf(`{"symbol":%q,"side":%q,"type":%q,"qty":%.8f,"price":%.8f}`, req.Symbol, req.Side, req.OrderType, req.Quantity, req.Price)
	switch venue {
	case VenueBinance:
		q := url.Values{
			"symbol":    {req.Symbol},
			"side":      {req.Side},
			"type":      {req.OrderType},
			"quantity":  {fmt.Sprintf("%.8f", req.Quantity)},
			"timestamp": {tsMs},
		}.Encode()
		rawSig = SignBinanceHMAC(secret, q)
	case VenueBybit:
		rawSig = SignBybitV5(secret, tsMs, apiKey, "5000", payload)
	case VenueOKX:
		rawSig = SignOKXV5(secret, tsMs, "POST", "/api/v5/trade/order", payload)
	case VenueKraken:
		sig, err := SignKraken(secret, "/0/private/AddOrder", tsMs, "nonce="+tsMs+"&pair="+url.QueryEscape(req.Symbol))
		if err != nil {
			return "", err
		}
		rawSig = sig
	case VenueCoinbase:
		rawSig = SignCoinbaseHMAC(secret, tsMs, "POST", "/api/v3/brokerage/orders", payload)
	case VenueDeribit:
		if sig, err := SignDeribitEd25519(secret, tsMs, req.ClientOrderID, "POST", "/api/v2/private/"+strings.ToLower(req.Side), payload); err == nil {
			rawSig = sig
		} else {
			// Fallback deterministic HMAC digest when secret is a sandbox HMAC token rather than raw Ed25519 seed.
			rawSig = SignCoinbaseHMAC(secret, tsMs, "POST", "/api/v2/private/order", payload)
		}
	default:
		return "", errors.New("unsupported cex venue")
	}
	sum := sha256.Sum256([]byte(rawSig))
	return "0x" + hex.EncodeToString(sum[:]), nil
}
