package queue

import (
	"encoding/json"
	"errors"
	"fmt"
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
	maxRedactedLeaf = 512
	maxLeaves       = 256
)

var (
	// userinfo in URLs: scheme://user:password@host
	urlCredentials = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`)
	// password=..., "token": "...", api_key: ...
	secretAssignment = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|authorization|credential)s?\b(["']?\s*[:=]\s*["']?)([^\s"',;]+)`)
	// Authorization header values
	bearerToken = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
)

// sanitizeError turns a handler error into text that is safe to store and
// log: "<root error type>: <message>" where the message has had the strings
// of the job's payload, URL credentials, obvious secret assignments and
// bearer tokens replaced by [redacted], control characters removed and the
// result cut to MaxErrorBytes.
//
// This is defense in depth, not a guarantee. Handlers should return errors
// that describe what failed without echoing their input.
func sanitizeError(err error, payload []byte) string {
	if err == nil {
		return ""
	}
	message := redactPayloadStrings(err.Error(), payload)
	message = urlCredentials.ReplaceAllString(message, "${1}"+redacted+"@")
	message = bearerToken.ReplaceAllString(message, "${1} "+redacted)
	message = secretAssignment.ReplaceAllString(message, "${1}${2}"+redacted)
	text := rootType(err) + ": " + message
	return truncateText(cleanText(text), MaxErrorBytes)
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
// JSON payload that is long enough to be distinctive.
func redactPayloadStrings(message string, payload []byte) string {
	leaves := payloadStrings(payload)
	if len(leaves) == 0 {
		return message
	}
	// Longest first, so a leaf that contains another is replaced whole.
	slices.SortFunc(leaves, func(a, b string) int { return len(b) - len(a) })
	for _, leaf := range leaves {
		message = strings.ReplaceAll(message, leaf, redacted)
		// The same text may appear JSON-escaped in a decoder error.
		if quoted, err := json.Marshal(leaf); err == nil && len(quoted) > 2 {
			message = strings.ReplaceAll(message, string(quoted[1:len(quoted)-1]), redacted)
		}
	}
	return message
}

func payloadStrings(payload []byte) []string {
	if len(payload) == 0 {
		return nil
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil
	}
	var leaves []string
	seen := make(map[string]struct{})
	var walk func(value any)
	walk = func(value any) {
		if len(leaves) >= maxLeaves {
			return
		}
		switch v := value.(type) {
		case string:
			if len(v) < minRedactedLeaf || len(v) > maxRedactedLeaf {
				return
			}
			if _, dup := seen[v]; !dup {
				seen[v] = struct{}{}
				leaves = append(leaves, v)
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
	return leaves
}

// cleanText drops control characters and invalid UTF-8 and collapses line
// breaks to spaces, so stored text is one printable line.
func cleanText(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			out.WriteByte(' ')
		case r == utf8.RuneError || r < 0x20 || r == 0x7f:
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
