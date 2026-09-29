package platform

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"time"
)

// Clock abstracts time for tests.
type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// RandomHex returns n random bytes hex-encoded.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// RandomToken returns a URL-safe random token with n bytes of entropy.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// RandomPassword returns an alphanumeric secret safe for SQL literals, env
// files, and URLs.
func RandomPassword(length int) string {
	out := make([]byte, length)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		out[i] = passwordAlphabet[n.Int64()]
	}
	return string(out)
}

// NewAppID allocates a random immutable app incarnation identity.
func NewAppID() string { return "a" + RandomHex(6) }

// NewOperationID allocates an operation identifier.
func NewOperationID() string { return "op_" + RandomHex(10) }

// NewStackID allocates a stable stack identity.
func NewStackID() string { return "s" + RandomHex(8) }

// SHA256Hex hashes b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SHA256HexConcat hashes the concatenation of parts without building it: the
// result equals SHA256Hex of the joined bytes, so fingerprints that used the
// joined form are unchanged.
func SHA256HexConcat(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p) // hash.Hash.Write never returns an error
	}
	var sum [sha256.Size]byte
	return hex.EncodeToString(h.Sum(sum[:0]))
}

// FormatTime is the single wire/storage timestamp representation: RFC 3339 UTC
// with millisecond precision.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func ParseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
