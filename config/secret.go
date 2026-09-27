package config

import "log/slog"

const redacted = "[REDACTED]"

// Secret holds a sensitive value such as a database URL. Every formatting,
// logging and serialization path prints [REDACTED]; only Reveal returns the
// value, which makes each use explicit and easy to audit.
type Secret struct{ value string }

func NewSecret(value string) Secret { return Secret{value: value} }

// Reveal returns the underlying value. Pass it straight to the component that
// needs it; never log or return it to clients.
func (s Secret) Reveal() string { return s.value }

func (s Secret) IsZero() bool { return s.value == "" }

func (Secret) String() string { return redacted }

func (Secret) GoString() string { return redacted }

func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
