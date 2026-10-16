package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	redacted = "[redacted]"
	// minRedactedLeaf is the shortest payload string removed from error text;
	// shorter strings ("ok", "id") would shred legitimate messages.
	minRedactedLeaf = 4
	// longLeaf is the length above which a payload string is also matched by
	// any substring of windowSize bytes or more, so an error that quotes only
	// part of a long token (a library truncating it, say) still loses it.
	longLeaf   = 48
	windowSize = 32
	// maxWindows bounds the memory spent on substring matching: the windows of
	// the longest strings first, up to this many.
	maxWindows = 1 << 18
	// maxNeedles bounds redaction work per failure. When a payload holds more
	// distinct strings, the longest are kept: they are the likeliest secrets
	// and the most distinctive.
	maxNeedles = 8192
	// maxRedactInput bounds the error text redaction scans; the stored result
	// is far shorter, so the cut is never visible.
	maxRedactInput = 16 << 10
)

var (
	// userinfo in URLs: scheme://user:password@host
	urlCredentials = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`)
	// password=..., "token": "...", api_key: ...
	secretAssignment = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|authorization|credential)s?\b(["']?\s*[:=]\s*["']?)([^\s"',;]+)`)
	// Authorization header values
	bearerToken = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
)

// unsafeRune reports whether r must never reach a terminal or a log viewer
// unescaped: C0 and C1 control characters (the C1 range includes the 8-bit
// CSI, U+009B), bidirectional overrides and isolates that reorder displayed
// text, line and paragraph separators, the byte order mark and the Unicode
// replacement character that marks invalid input.
func unsafeRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, r >= 0x202a && r <= 0x202e,
		r == 0x2028, r == 0x2029, r >= 0x2066 && r <= 0x2069, r == 0xfeff, r == utf8.RuneError:
		return true
	}
	return false
}

// EscapeUnsafe returns text with every control character, bidirectional
// override and invalid byte replaced by a visible \uXXXX or \xNN escape, so
// stored text can be printed to a terminal without being able to drive it.
func EscapeUnsafe(text string) string {
	if !strings.ContainsFunc(text, unsafeRune) && utf8.ValidString(text) {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&out, `\x%02x`, text[i])
		case unsafeRune(r):
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			out.WriteString(text[i : i+size])
		}
		i += size
	}
	return out.String()
}

// sanitizeError turns a handler error into text that is safe to store and
// log: "<root error type>: <message>" where the message has had the strings
// of the job's payload, URL credentials, obvious secret assignments and
// bearer tokens replaced by [redacted], control characters removed and the
// result cut to MaxErrorBytes. An error whose methods panic (a typed nil, say)
// yields "<type>: error text unavailable" instead of crashing the worker.
//
// This is defense in depth, not a guarantee. Handlers should return errors
// that describe what failed without echoing their input.
func sanitizeError(err error, payload []byte) (text string) {
	if err == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			text = fmt.Sprintf("%T", err) + ": error text unavailable"
		}
	}()
	message := truncateText(err.Error(), maxRedactInput)
	message = redactPayloadStrings(message, payload)
	message = urlCredentials.ReplaceAllString(message, "${1}"+redacted+"@")
	message = bearerToken.ReplaceAllString(message, "${1} "+redacted)
	message = secretAssignment.ReplaceAllString(message, "${1}${2}"+redacted)
	return truncateText(cleanText(rootType(err)+": "+message), MaxErrorBytes)
}

// rootType names the innermost error type, which says what failed without
// quoting any message.
func rootType(err error) string {
	root := err
	for {
		if joined, ok := root.(interface{ Unwrap() []error }); ok {
			if inner := joined.Unwrap(); len(inner) > 0 {
				root = inner[0]
				continue
			}
		}
		next := errors.Unwrap(root)
		if next == nil {
			break
		}
		root = next
	}
	return fmt.Sprintf("%T", root)
}

// redactPayloadStrings replaces, in message, every string value found in the
// JSON payload that is long enough to be distinctive, longest first so a value
// that contains another is replaced whole, and then any run of windowSize
// bytes or more copied from a long value.
func redactPayloadStrings(message string, payload []byte) string {
	needles, long := payloadNeedles(payload)
	for _, needle := range needles {
		message = strings.ReplaceAll(message, needle, redacted)
	}
	return redactWindows(message, long)
}

// payloadNeedles returns the distinct string values worth matching whole
// (longest first, at most maxNeedles) and the long ones among them.
func payloadNeedles(payload []byte) (needles, long []string) {
	if len(payload) == 0 {
		return nil, nil
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var walk func(value any)
	walk = func(value any) {
		switch v := value.(type) {
		case string:
			if len(v) < minRedactedLeaf {
				return
			}
			seen[v] = struct{}{}
			// The same text may appear JSON-escaped in a decoder error.
			if quoted, err := json.Marshal(v); err == nil && len(quoted) > 2 {
				if escaped := string(quoted[1 : len(quoted)-1]); escaped != v {
					seen[escaped] = struct{}{}
				}
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any: // keys are schema, not data: only values are redacted
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(document)
	needles = make([]string, 0, len(seen))
	for needle := range seen {
		needles = append(needles, needle)
	}
	// Longest first, ties broken by content: the order, and with it what is
	// kept past maxNeedles, never depends on map iteration.
	slices.SortFunc(needles, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	if len(needles) > maxNeedles {
		needles = needles[:maxNeedles]
	}
	for _, needle := range needles {
		if len(needle) > longLeaf {
			long = append(long, needle)
		}
	}
	return needles, long
}

// redactWindows replaces every run of at least windowSize bytes in message
// that also occurs in one of the long values. Windows are compared by 64-bit
// hash: a collision redacts a little extra text, never less.
func redactWindows(message string, long []string) string {
	if len(long) == 0 || len(message) < windowSize {
		return message
	}
	seed := maphash.MakeSeed()
	windows := make(map[uint64]struct{})
	for _, value := range long { // longest first
		for i := 0; i+windowSize <= len(value) && len(windows) < maxWindows; i++ {
			windows[maphash.String(seed, value[i:i+windowSize])] = struct{}{}
		}
	}
	covered := make([]bool, len(message))
	any := false
	for i := 0; i+windowSize <= len(message); i++ {
		if _, ok := windows[maphash.String(seed, message[i:i+windowSize])]; ok {
			any = true
			for k := i; k < i+windowSize; k++ {
				covered[k] = true
			}
		}
	}
	if !any {
		return message
	}
	var out strings.Builder
	for i := 0; i < len(message); {
		if !covered[i] {
			out.WriteByte(message[i])
			i++
			continue
		}
		for i < len(message) && covered[i] {
			i++
		}
		out.WriteString(redacted)
	}
	return out.String()
}

// cleanText drops unsafe characters and invalid UTF-8 and collapses line
// breaks to spaces, so stored text is one printable line.
func cleanText(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			out.WriteByte(' ')
		case unsafeRune(r):
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// truncateText cuts text to at most limit bytes on a rune boundary, marking
// the cut.
func truncateText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const marker = "..."
	cut := limit - len(marker)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + marker
}
