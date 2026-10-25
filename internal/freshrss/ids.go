package freshrss

import (
	"errors"
	"math/big"
	"strings"
)

// ItemPrefix is the Google Reader item ID prefix used by FreshRSS.
const ItemPrefix = "tag:google.com,2005:reader/item/"

// ParseItemKey normalizes any accepted item identifier to FreshRSS' internal
// decimal ID, which is the canonical key used across the application.
//
// Accepted inputs: "tag:google.com,2005:reader/item/00065eab4fd67499",
// "00065eab4fd67499" (hex) is *not* accepted, because it is ambiguous with a
// decimal ID; inside the prefix it is treated as hex.
func ParseItemKey(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("empty item id")
	}
	if strings.HasPrefix(s, ItemPrefix) {
		hexPart := strings.TrimPrefix(s, ItemPrefix)
		if hexPart == "" || len(hexPart) > 32 {
			return "", errors.New("invalid item id")
		}
		n, ok := new(big.Int).SetString(hexPart, 16)
		if !ok || n.Sign() < 0 {
			return "", errors.New("invalid item id")
		}
		return n.String(), nil
	}
	if !isDigits(s) {
		return "", errors.New("invalid item id")
	}
	return strings.TrimLeft(s, "0"), nil
}

// FormatItemKey renders a decimal item ID as a Google Reader item ID.
func FormatItemKey(decimal string) (string, error) {
	if decimal == "" || !isDigits(decimal) {
		return "", errors.New("invalid decimal item id")
	}
	n, ok := new(big.Int).SetString(strings.TrimLeft(decimal, "0"), 10)
	if !ok {
		return "", errors.New("invalid decimal item id")
	}
	return ItemPrefix + hexPad16(n), nil
}

func hexPad16(n *big.Int) string {
	h := n.Text(16)
	if len(h) < 16 {
		h = strings.Repeat("0", 16-len(h)) + h
	}
	return h
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
