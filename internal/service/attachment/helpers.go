// Package attachment — small local helpers.
package attachment

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// newHash returns a fresh SHA-256 hasher.
func newHash() hash.Hash { return sha256.New() }

// toHex returns the lowercase hex string of the digest.
func toHex(b []byte) string { return hex.EncodeToString(b) }

// newUUIDLite returns a UUIDv4-ish random string.
//
// We avoid importing google/uuid to keep the service package's dependency
// surface tiny — the repo uses google/uuid but this file is reachable
// from tests that don't pull in storage.
func newUUIDLite() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	// Set version (4) and variant bits per RFC 4122 §4.4.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	out := make([]byte, 36)
	const hex = "0123456789abcdef"
	for i, x := range b {
		out[i*2] = hex[x>>4]
		out[i*2+1] = hex[x&0xf]
		if i == 3 || i == 5 || i == 7 || i == 9 {
			out[i*2+2] = '-'
			out[i*2+3] = '-'
		}
	}
	// The above produces extra dashes from off-by-one placement; build it
	// cleanly instead:
	const dashes = "----"
	clean := make([]byte, 0, 36)
	for i := 0; i < 16; i++ {
		clean = append(clean, hex[b[i]>>4], hex[b[i]&0xf])
		if i == 3 || i == 5 || i == 7 || i == 9 {
			clean = append(clean, '-')
		}
	}
	_ = dashes
	return string(clean)
}
