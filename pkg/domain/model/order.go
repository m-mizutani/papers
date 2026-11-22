package model

import (
	"strings"

	"github.com/m-mizutani/goerr/v2"
)

// Fractional index keys. A key is a non-empty string over orderDigits that
// does not end with the smallest digit; such keys are dense, so a key always
// exists strictly between any two distinct keys. This is the midpoint
// scheme popularised by Figma / rocicorp's fractional-indexing.

const orderDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ValidOrderKey reports whether k is a well-formed key.
func ValidOrderKey(k string) bool {
	if k == "" || k[len(k)-1] == orderDigits[0] {
		return false
	}
	for i := 0; i < len(k); i++ {
		if strings.IndexByte(orderDigits, k[i]) < 0 {
			return false
		}
	}
	return true
}

// KeyBetween returns a key strictly between a and b. An empty a means "before
// everything", an empty b means "after everything".
func KeyBetween(a, b string) (string, error) {
	if a != "" && !ValidOrderKey(a) {
		return "", goerr.Wrap(ErrInvalidArgument, "invalid lower order key", goerr.V("key", a))
	}
	if b != "" && !ValidOrderKey(b) {
		return "", goerr.Wrap(ErrInvalidArgument, "invalid upper order key", goerr.V("key", b))
	}
	if a != "" && b != "" && a >= b {
		return "", goerr.Wrap(ErrInvalidArgument, "lower key must be less than upper key", goerr.V("a", a), goerr.V("b", b))
	}
	return midpoint(a, b), nil
}

// midpoint assumes a < b (with "" b meaning +infinity) and both valid or "".
func midpoint(a, b string) string {
	if b != "" {
		// Skip the common prefix, padding a with the zero digit.
		n := 0
		for n < len(b) {
			ca := orderDigits[0]
			if n < len(a) {
				ca = a[n]
			}
			if ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			rest := ""
			if n < len(a) {
				rest = a[n:]
			}
			return b[:n] + midpoint(rest, b[n:])
		}
	}

	digitA := 0
	if a != "" {
		digitA = strings.IndexByte(orderDigits, a[0])
	}
	digitB := len(orderDigits)
	if b != "" {
		digitB = strings.IndexByte(orderDigits, b[0])
	}

	if digitB-digitA > 1 {
		return string(orderDigits[(digitA+digitB+1)/2])
	}
	// The first digits are consecutive.
	if b != "" && len(b) > 1 {
		return b[:1]
	}
	rest := ""
	if a != "" {
		rest = a[1:]
	}
	return string(orderDigits[digitA]) + midpoint(rest, "")
}
