package config

import (
	netmail "net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file holds the single definition of what Forge accepts as a mailbox.
// It lives in config, a package every core package may import, so that
// configuration validation (FORGE_MAIL_FROM) and the mail package apply
// exactly the same rules instead of two copies that drift apart.

const (
	maxMailboxBytes = 254 // RFC 5321 path limit minus the angle brackets
	maxLocalBytes   = 64
	maxDomainBytes  = 253
	maxLabelBytes   = 63
	maxNameRunes    = 255
)

// MailboxError explains why a mailbox cannot be used. Reason is a static
// sentence; it never contains the offending value.
type MailboxError struct {
	Reason string
	// Unsupported marks input that is valid in the wild but outside what
	// Forge implements: addresses with non-ASCII characters (SMTPUTF8 and
	// IDNA are not supported). Callers can report it distinctly.
	Unsupported bool
}

func (e *MailboxError) Error() string { return e.Reason }

func mailboxErr(reason string) error { return &MailboxError{Reason: reason} }

// ValidateMailbox checks a display name and an addr-spec.
//
// The address must be ASCII dot-atom `local@domain` with an LDH host name;
// quoted local parts, comments, route addresses, domain literals and display
// forms are rejected, which removes the ambiguity used to smuggle recipients
// or SMTP commands. The display name may be any text without control,
// line-break or bidirectional-override characters.
func ValidateMailbox(name, email string) error {
	if email == "" {
		return mailboxErr("email is required")
	}
	if problem := textProblem(name); problem != "" {
		return mailboxErr("display name " + problem)
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return mailboxErr("display name is too long")
	}
	if problem := textProblem(email); problem != "" {
		return mailboxErr("email " + problem)
	}
	if len(email) > maxMailboxBytes {
		return mailboxErr("email is too long")
	}
	for i := 0; i < len(email); i++ {
		switch {
		case email[i] > unicode.MaxASCII:
			return &MailboxError{
				Reason:      "email has non-ASCII characters (internationalised addresses are not supported)",
				Unsupported: true,
			}
		case email[i] == ' ':
			return mailboxErr("email must not contain spaces")
		}
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || strings.Contains(domain, "@") {
		return mailboxErr("email must contain exactly one @")
	}
	if !validLocalPart(local) {
		return mailboxErr("email local part is not a plain dot-atom")
	}
	if !ValidMailDomain(domain) {
		return mailboxErr("email domain is not a valid host name")
	}
	// net/mail is the parser applications and mail servers agree on; the
	// address must survive it unchanged (no quoting, no display name).
	parsed, err := netmail.ParseAddress(email)
	if err != nil || parsed.Name != "" || parsed.Address != email {
		return mailboxErr("email is not a plain addr-spec")
	}
	return nil
}

// ParseMailbox parses "user@example.com" or "Display Name <user@example.com>"
// with net/mail and validates the result with ValidateMailbox.
func ParseMailbox(value string) (name, email string, err error) {
	if problem := textProblem(value); problem != "" {
		return "", "", mailboxErr("mailbox " + problem)
	}
	parsed, parseErr := netmail.ParseAddress(value)
	if parseErr != nil {
		// net/mail quotes the input; never surface it.
		return "", "", mailboxErr("mailbox is not valid")
	}
	if err := ValidateMailbox(parsed.Name, parsed.Address); err != nil {
		return "", "", err
	}
	return parsed.Name, parsed.Address, nil
}

// ValidMailDomain reports whether domain is an LDH host name of up to 253
// bytes. A single label such as "localhost" is allowed for development.
func ValidMailDomain(domain string) bool {
	if domain == "" || len(domain) > maxDomainBytes {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > maxLabelBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// validLocalPart accepts RFC 5322 dot-atom: atext separated by single dots.
func validLocalPart(local string) bool {
	if local == "" || len(local) > maxLocalBytes {
		return false
	}
	if local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") {
		return false
	}
	for i := 0; i < len(local); i++ {
		if !isAtext(local[i]) && local[i] != '.' {
			return false
		}
	}
	return true
}

func isAtext(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-/=?^_`{|}~", c) >= 0
}

// textProblem returns a reason when s has a character that could end or split
// a header line (CR, LF, NUL and every other control character, the Unicode
// line and paragraph separators), reorder displayed text (bidirectional
// overrides and isolates) or is not valid UTF-8; it returns "" otherwise.
func textProblem(s string) string {
	for _, r := range s {
		if unicode.IsControl(r) || isBidiControl(r) || r == '\u2028' || r == '\u2029' || r == utf8.RuneError {
			return "contains a control, line-break or direction-override character"
		}
	}
	return ""
}

func isBidiControl(r rune) bool {
	switch {
	case r >= '\u202a' && r <= '\u202e', r >= '\u2066' && r <= '\u2069':
		return true
	}
	return r == '\u200e' || r == '\u200f' || r == '\u061c'
}
