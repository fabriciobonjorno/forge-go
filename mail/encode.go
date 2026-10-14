package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"slices"
	"strings"
	"time"

	"github.com/fabriciobonjorno/forge-go/uuid"
)

// Encoded is a message ready for transport: the RFC 5322 bytes plus the SMTP
// envelope, which differs from the headers (Bcc recipients appear only in the
// envelope).
type Encoded struct {
	// MessageID includes the angle brackets: <uuidv7@sender-domain>.
	MessageID string
	// Sender is the envelope sender (MAIL FROM), the From address.
	Sender string
	// Recipients is the envelope recipient list (RCPT TO), de-duplicated.
	Recipients []string
	// Data is the CRLF-terminated message. It never contains a Bcc header.
	Data []byte
}

// EncodeOption customises Encode, mostly for deterministic tests.
type EncodeOption func(*encoder)

// WithLimits sets the size limits. Zero fields keep their defaults.
func WithLimits(limits Limits) EncodeOption {
	return func(e *encoder) { e.limits = limits.withDefaults() }
}

// WithClock sets the clock used for the Date header.
func WithClock(now func() time.Time) EncodeOption {
	return func(e *encoder) {
		if now != nil {
			e.now = now
		}
	}
}

// WithIDGenerator sets the UUIDv7 generator used for the Message-ID.
func WithIDGenerator(generator *uuid.Generator) EncodeOption {
	return func(e *encoder) {
		if generator != nil {
			e.ids = generator
		}
	}
}

// WithBoundaryEntropy sets the randomness used for MIME boundaries.
func WithBoundaryEntropy(r io.Reader) EncodeOption {
	return func(e *encoder) {
		if r != nil {
			e.entropy = r
		}
	}
}

type encoder struct {
	limits  Limits
	now     func() time.Time
	ids     *uuid.Generator
	entropy io.Reader
}

// Encode validates msg and renders it as an RFC 5322 / MIME message.
//
// Structure, from simplest to richest:
//
//	text only          text/plain
//	html only          text/html
//	text and html      multipart/alternative
//	inline images      multipart/related around the HTML part
//	file attachments   multipart/mixed around the body
//
// Bodies are quoted-printable UTF-8 and attachments base64, so the output is
// 7-bit clean. Date is UTC, Message-ID is a UUIDv7 at the sender's domain,
// non-ASCII names and subjects are RFC 2047 encoded and long headers are
// folded below 78 columns.
func Encode(msg Message, options ...EncodeOption) (Encoded, error) {
	e := &encoder{limits: DefaultLimits(), now: time.Now, entropy: rand.Reader}
	for _, option := range options {
		option(e)
	}
	if err := msg.validate(e.limits); err != nil {
		return Encoded{}, err
	}
	var id uuid.UUID
	var err error
	if e.ids != nil {
		id, err = e.ids.New()
	} else {
		id, err = uuid.New()
	}
	if err != nil {
		return Encoded{}, fmt.Errorf("mail: generate message id: %w", err)
	}
	messageID := "<" + id.String() + "@" + strings.ToLower(msg.From.Domain()) + ">"

	root, err := e.body(msg)
	if err != nil {
		return Encoded{}, err
	}

	var out bytes.Buffer
	header := func(name, value string) {
		out.WriteString(foldHeader(name, value))
		out.WriteString("\r\n")
	}
	header("Date", e.now().UTC().Format(time.RFC1123Z))
	header("From", msg.From.String())
	if len(msg.ReplyTo) > 0 {
		header("Reply-To", strings.Join(formatAddresses(msg.ReplyTo), ", "))
	}
	if len(msg.To) > 0 {
		header("To", strings.Join(formatAddresses(msg.To), ", "))
	}
	if len(msg.Cc) > 0 {
		header("Cc", strings.Join(formatAddresses(msg.Cc), ", "))
	}
	header("Subject", encodeText(msg.Subject))
	header("Message-ID", messageID)
	header("MIME-Version", "1.0")
	names := make([]string, 0, len(msg.Headers))
	canonical := make(map[string]string, len(msg.Headers))
	for name, value := range msg.Headers {
		key := textproto.CanonicalMIMEHeaderKey(name)
		names = append(names, key)
		canonical[key] = value
	}
	slices.Sort(names)
	for _, name := range names {
		header(name, canonical[name])
	}
	for _, name := range sortedKeys(root.header) {
		header(name, root.header[name][0])
	}
	out.WriteString("\r\n")
	out.Write(root.body)
	if !bytes.HasSuffix(out.Bytes(), []byte("\r\n")) {
		out.WriteString("\r\n")
	}
	return Encoded{
		MessageID:  messageID,
		Sender:     msg.From.Email,
		Recipients: msg.Recipients(),
		Data:       out.Bytes(),
	}, nil
}

// entity is one MIME entity: its headers and its already-encoded body.
type entity struct {
	header textproto.MIMEHeader
	body   []byte
}

func (e *encoder) body(msg Message) (entity, error) {
	var inline, files []Attachment
	for _, attachment := range msg.Attachments {
		if attachment.ContentID != "" {
			inline = append(inline, attachment)
		} else {
			files = append(files, attachment)
		}
	}

	var content entity
	switch {
	case msg.Text != "" && msg.HTML != "":
		text := quotedPrintablePart("text/plain", msg.Text)
		html := quotedPrintablePart("text/html", msg.HTML)
		if len(inline) > 0 {
			related, err := e.multipart("related", e.attachmentEntities(html, inline, true))
			if err != nil {
				return entity{}, err
			}
			html = related
		}
		alternative, err := e.multipart("alternative", []entity{text, html})
		if err != nil {
			return entity{}, err
		}
		content = alternative
	case msg.HTML != "":
		html := quotedPrintablePart("text/html", msg.HTML)
		if len(inline) > 0 {
			related, err := e.multipart("related", e.attachmentEntities(html, inline, true))
			if err != nil {
				return entity{}, err
			}
			html = related
		}
		content = html
	default:
		content = quotedPrintablePart("text/plain", msg.Text)
	}
	if len(files) == 0 {
		return content, nil
	}
	return e.multipart("mixed", e.attachmentEntities(content, files, false))
}

func (e *encoder) attachmentEntities(first entity, attachments []Attachment, inline bool) []entity {
	parts := []entity{first}
	for _, attachment := range attachments {
		parts = append(parts, attachmentEntity(attachment, inline))
	}
	return parts
}

func (e *encoder) multipart(subtype string, parts []entity) (entity, error) {
	boundary, err := e.boundary()
	if err != nil {
		return entity{}, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary(boundary); err != nil {
		return entity{}, err
	}
	for _, part := range parts {
		w, err := writer.CreatePart(part.header)
		if err != nil {
			return entity{}, err
		}
		if _, err := w.Write(part.body); err != nil {
			return entity{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return entity{}, err
	}
	body.WriteString("\r\n")
	contentType := mime.FormatMediaType("multipart/"+subtype, map[string]string{"boundary": boundary})
	if contentType == "" {
		return entity{}, errors.New("mail: format multipart content type")
	}
	return entity{header: textproto.MIMEHeader{"Content-Type": {contentType}}, body: body.Bytes()}, nil
}

// boundary returns an unguessable boundary. 128 random bits make a collision
// with body content (quoted-printable and base64 cannot contain "--" lines
// of that shape by chance) astronomically unlikely.
func (e *encoder) boundary() (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(e.entropy, raw[:]); err != nil {
		return "", fmt.Errorf("mail: generate boundary: %w", err)
	}
	return "forge-" + hex.EncodeToString(raw[:]), nil
}

func quotedPrintablePart(mediaType, text string) entity {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var body bytes.Buffer
	writer := quotedprintable.NewWriter(&body)
	// Writes to a bytes.Buffer cannot fail.
	_, _ = writer.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n")))
	_ = writer.Close()
	if !bytes.HasSuffix(body.Bytes(), []byte("\r\n")) {
		body.WriteString("\r\n")
	}
	return entity{
		header: textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(mediaType, map[string]string{"charset": "utf-8"})},
			"Content-Transfer-Encoding": {"quoted-printable"},
		},
		body: body.Bytes(),
	}
}

func attachmentEntity(attachment Attachment, inline bool) entity {
	// Validated by Message.validate; the error paths cannot occur here.
	filename, _ := SanitizeFilename(attachment.Filename)
	contentType, _ := attachmentContentType(attachment.ContentType)
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	header := textproto.MIMEHeader{
		"Content-Type": {mime.FormatMediaType(contentType, map[string]string{"name": filename})},
		// FormatMediaType applies RFC 2231 encoding to non-ASCII names.
		"Content-Disposition":       {mime.FormatMediaType(disposition, map[string]string{"filename": filename})},
		"Content-Transfer-Encoding": {"base64"},
	}
	if attachment.ContentID != "" {
		header["Content-Id"] = []string{"<" + attachment.ContentID + ">"}
	}
	encoded := base64.StdEncoding.EncodeToString(attachment.Data)
	var body bytes.Buffer
	for len(encoded) > 76 {
		body.WriteString(encoded[:76])
		body.WriteString("\r\n")
		encoded = encoded[76:]
	}
	body.WriteString(encoded)
	body.WriteString("\r\n")
	return entity{header: header, body: body.Bytes()}
}

// foldHeader renders "Name: value" with lines folded at spaces so none
// exceeds 78 columns where a space allows. Folding inserts CRLF before an
// existing run of spaces, which unfolding removes again, so the value is
// preserved exactly; values never contain CR or LF (validated), so no extra
// line can appear.
//
// Two invariants protect against gateways that strip trailing whitespace:
// no line ends in whitespace (a run of spaces always moves to the start of
// the next line, in front of a word), and no line is whitespace-only (a
// stripped blank line would end the header block early). Leading and
// trailing spaces of the value are not significant and are dropped.
func foldHeader(name, value string) string {
	const limit = 76
	value = strings.Trim(value, " ")
	line := name + ":"
	if value == "" {
		return line
	}
	var out strings.Builder
	first := true
	for rest := value; rest != ""; {
		gap := rest[:len(rest)-len(strings.TrimLeft(rest, " "))]
		rest = rest[len(gap):]
		word := rest
		if i := strings.IndexByte(rest, ' '); i >= 0 {
			word = rest[:i]
		}
		rest = rest[len(word):]
		if first {
			gap = " "
		}
		switch {
		case first && len(line)+len(gap)+len(word) > limit && len(word) > limit/2:
			// A long first token (an RFC 2047 word) goes on its own line.
			out.WriteString(line)
			out.WriteString("\r\n")
			line = gap + word
		case !first && len(line)+len(gap)+len(word) > limit:
			out.WriteString(line)
			out.WriteString("\r\n")
			line = gap + word
		default:
			line += gap + word
		}
		first = false
	}
	out.WriteString(line)
	return out.String()
}

func sortedKeys(h textproto.MIMEHeader) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
