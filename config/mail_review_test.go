package config_test

import (
	"strings"
	"testing"
)

// FORGE_MAIL_FROM must pass the same strict rules the mail package applies to
// every address; otherwise the application starts and then fails every send.
func TestMailFromUsesTheStrictAddressRules(t *testing.T) {
	t.Parallel()
	bad := []string{
		`"quoted local"@example.com`,
		"ação@example.com",
		"app@exämple.com",
		"Name\u202e <a@example.com>",
		"a@[127.0.0.1]",
		"a@example.com\r\nBcc: x@y.com",
		"a@exa_mple.com",
		"a..b@example.com",
		"<a@example.com> <b@example.com>",
		"a@example.com, b@example.com",
	}
	for _, from := range bad {
		_, err := loadMail(map[string]string{"FORGE_MAIL_URL": "smtp://u:p@h:587", "FORGE_MAIL_FROM": from})
		if err == nil {
			t.Errorf("FORGE_MAIL_FROM %q accepted", from)
			continue
		}
		if !strings.Contains(err.Error(), "FORGE_MAIL_FROM") || strings.Contains(err.Error(), "Bcc") {
			t.Errorf("error for %q = %v", from, err)
		}
	}
	for _, from := range []string{"app@example.com", "App <app@example.com>", "José Ação <app@example.com>", `"App, Inc" <app@example.com>`} {
		if _, err := loadMail(map[string]string{"FORGE_MAIL_URL": "smtp://u:p@h:587", "FORGE_MAIL_FROM": from}); err != nil {
			t.Errorf("FORGE_MAIL_FROM %q rejected: %v", from, err)
		}
	}
}

// A percent-encoded CRLF in the user name or password would be decoded by
// net/url and later written into an SMTP dialogue.
func TestMailURLCredentialsRejectControlCharacters(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"smtp://user:pa%0d%0aQUIT@smtp.example.com:587",
		"smtp://us%0aer:secret-pass@smtp.example.com:587",
		"smtps://user:pa%00ss@smtp.example.com",
		"smtp://user:pa%7fss@smtp.example.com",
		"smtp://user:pa%c2%85ss@smtp.example.com",
	} {
		_, err := loadMail(map[string]string{"FORGE_MAIL_URL": rawURL, "FORGE_MAIL_FROM": "a@example.com"})
		if err == nil {
			t.Errorf("%q accepted", rawURL)
			continue
		}
		for _, leak := range []string{"QUIT", "secret-pass", "pa%0d", "pass"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error for %q leaks %q: %v", rawURL, leak, err)
			}
		}
	}
}
