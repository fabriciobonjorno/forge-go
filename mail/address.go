package mail

import (
	"fmt"
	netmail "net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxAddressBytes = 254 // RFC 5321 path limit minus the angle brackets
	maxLocalBytes   = 64
	maxDomainBytes  = 253
	maxLabelBytes   = 63
	maxNameRunes    = 255
)

// Address is one mailbox: an optional display name and an ASCII addr-spec.
//
// Forge does not implement SMTPUTF8 (RFC 6531): the address itself must be
// ASCII, while the display name may be any text and is encoded per RFC 2047.
// Only the dot-atom form of the local part is accepted; quoted local parts,
// comments, route addresses and domain literals are rejected, which removes
// the ambiguity attackers use to smuggle extra recipients or commands.
type Address struct {
	Name  string
	Email string
}

// NewAddress validates and returns an address.
func NewAddress(name, email string) (Address, error) {
	address := Address{Name: name, Email: email}
	if err := address.Validate(); err != nil {
		return Address{}, err
	}
	return address, nil
}

// ParseAddress parses "user@example.com" or "Display Name <user@example.com>"
// with net/mail and applies Forge's stricter validation to the result.
func ParseAddress(value string) (Address, error) {
	if err := rejectControl("address", value); err != nil {
		return Address{}, err
	}
	parsed, err := netmail.ParseAddress(value)
	if err != nil {
		// net/mail quotes the input; never surface it.
		return Address{}, invalid("address", "is not a valid mailbox")
	}
	return NewAddress(parsed.Name, parsed.Address)
}

// Validate reports why the address cannot be used in a message.
func (a Address) Validate() error { return a.validate("address") }

// String returns the header form: name RFC 2047 encoded when needed, followed
// by <email>. An address without a name renders as the bare addr-spec. It is
// only meaningful for a valid address.
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return (&netmail.Address{Name: a.Name, Address: a.Email}).String()
}

// Domain returns the part after the "@".
func (a Address) Domain() string {
	_, domain, _ := strings.Cut(a.Email, "@")
	return domain
}

func (a Address) validate(field string) error {
	if a.Email == "" {
		return invalid(field, "email is required")
	}
	if err := rejectControl(field, a.Name); err != nil {
		return err
	}
	if utf8.RuneCountInString(a.Name) > maxNameRunes {
		return invalid(field, "display name is too long")
	}
	if !utf8.ValidString(a.Name) {
		return invalid(field, "display name is not valid UTF-8")
	}
	return validateAddrSpec(field, a.Email)
}

func validateAddrSpec(field, email string) error {
	if err := rejectControl(field, email); err != nil {
		return err
	}
	if len(email) > maxAddressBytes {
		return invalid(field, "email is too long")
	}
	for i := 0; i < len(email); i++ {
		if email[i] > unicode.MaxASCII || email[i] == ' ' {
			return invalid(field, "email must be ASCII without spaces (SMTPUTF8 is not supported)")
		}
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || strings.Contains(domain, "@") {
		return invalid(field, "email must contain exactly one @")
	}
	if !validLocalPart(local) {
		return invalid(field, "email local part is not a plain dot-atom")
	}
	if !validDomain(domain) {
		return invalid(field, "email domain is not a valid host name")
	}
	// net/mail is the parser applications and mail servers agree on; the
	// address must survive it unchanged (no quoting, no display name).
	parsed, err := netmail.ParseAddress(email)
	if err != nil || parsed.Name != "" || parsed.Address != email {
		return invalid(field, "email is not a plain addr-spec")
	}
	return nil
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

// validDomain accepts LDH host names (letters, digits, hyphens) of up to 253
// bytes. A single label such as "localhost" is allowed for development.
func validDomain(domain string) bool {
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

// rejectControl refuses every character that could end or split a header
// line (CR, LF, NUL and every other control character, plus the Unicode line
// and paragraph separators some clients treat as line breaks) and the
// bidirectional overrides and isolates, which reorder displayed text.
func rejectControl(field, value string) error {
	for _, r := range value {
		if unicode.IsControl(r) || isBidiControl(r) || r == '\u2028' || r == '\u2029' || r == utf8.RuneError {
			return invalid(field, "contains a control or line-break character")
		}
	}
	return nil
}

func addressList(field string, list []Address) error {
	for i, address := range list {
		if err := address.validate(fmt.Sprintf("%s[%d]", field, i)); err != nil {
			return err
		}
	}
	return nil
}

func formatAddresses(list []Address) []string {
	out := make([]string, len(list))
	for i, address := range list {
		out[i] = address.String()
	}
	return out
}
