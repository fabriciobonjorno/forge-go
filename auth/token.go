package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
)

const tokenBytes = 32

var ErrInvalidToken = errors.New("invalid session token")

// Token is the bearer secret returned once to a client. Persist Digest, never
// Token itself, so a database disclosure does not expose active credentials.
type Token struct {
	secret string
	digest Digest
}

// Digest is the SHA-256 lookup key persisted by a session store.
type Digest [sha256.Size]byte

func NewToken() (Token, error) { return NewTokenWithEntropy(rand.Reader) }

func NewTokenWithEntropy(entropy io.Reader) (Token, error) {
	if entropy == nil {
		return Token{}, errors.New("session token entropy source is required")
	}
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(entropy, raw); err != nil {
		return Token{}, err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	return Token{secret: secret, digest: sha256.Sum256(raw)}, nil
}

func ParseToken(secret string) (Token, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil || len(raw) != tokenBytes || base64.RawURLEncoding.EncodeToString(raw) != secret {
		return Token{}, ErrInvalidToken
	}
	return Token{secret: secret, digest: sha256.Sum256(raw)}, nil
}

// Reveal returns the bearer credential for delivery to the client. Token does
// formats and logs as redacted so generic diagnostics cannot reveal it.
func (t Token) Reveal() string { return t.secret }

func (Token) String() string { return "[REDACTED]" }

func (Token) GoString() string { return "auth.Token([REDACTED])" }

func (Token) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

func (t Token) Digest() Digest { return t.digest }
