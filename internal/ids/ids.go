// Package ids mints identifiers and computes the hashes used across Wikory.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var ulidRe = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// ULID returns a 26-character Crockford base32 ULID: 48 bits of milliseconds, 80 random bits.
func ULID(now time.Time, entropy io.Reader) (string, error) {
	ms := now.UnixMilli()
	if ms < 0 || ms >= 1<<48 {
		return "", errors.New("ids: time outside the ULID range")
	}
	var b [16]byte
	for i := 0; i < 6; i++ {
		b[i] = byte(uint64(ms) >> (40 - 8*i))
	}
	if _, err := io.ReadFull(entropy, b[6:]); err != nil {
		return "", err
	}
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:]), nil
}

// NewULID mints a ULID from the clock and crypto/rand.
func NewULID() string {
	s, err := ULID(time.Now(), rand.Reader)
	if err != nil {
		panic("ids: " + err.Error()) // crypto/rand failing is unrecoverable
	}
	return s
}

// IsULID reports whether s is a well-formed ULID.
func IsULID(s string) bool { return ulidRe.MatchString(s) }

// ProjectID mints the committed project identity (spec 4.1).
func ProjectID() string { return "wkp_" + NewULID() }

// JobID mints a publish or consolidation job id.
func JobID() string { return "job_" + NewULID() }

// SHA256Hex is the full hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Digest hashes the parts with a length prefix on each, so ("ab","c") and ("a","bc") differ.
func Digest(parts ...string) string {
	h := sha256.New()
	var n [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
