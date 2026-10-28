package tron

import (
	"crypto/sha256"
	"errors"
	"github.com/mr-tron/base58"
)

func decodeAddress(s string) ([]byte, error) {
	b, err := base58.Decode(s)
	if err != nil || len(b) != 25 {
		return nil, errors.New("invalid TRON address")
	}
	h1 := sha256.Sum256(b[:21])
	h2 := sha256.Sum256(h1[:])
	for i := 0; i < 4; i++ {
		if b[21+i] != h2[i] {
			return nil, errors.New("invalid TRON address checksum")
		}
	}
	if b[0] != 0x41 {
		return nil, errors.New("invalid TRON address prefix")
	}
	return b[:21], nil
}

func ValidAddress(s string) bool { _, err := decodeAddress(s); return err == nil }
