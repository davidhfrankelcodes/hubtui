package hub

import (
	"fmt"
	"io"
)

// Secret holds a credential. It formats as "[redacted]" under every verb,
// including %v, %+v and %#v of a struct that contains it, so a stray log or
// error message cannot leak the token.
type Secret string

const redacted = "[redacted]"

// Format implements fmt.Formatter.
func (Secret) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, redacted) }

// MarshalText keeps the secret out of JSON and similar encodings too.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Reveal returns the raw value, for the one place that must send it.
func (s Secret) Reveal() string { return string(s) }
