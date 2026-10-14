package config_test

import (
	"strings"
	"testing"
)

func TestProductionRejectsPlaintextOverrideWithRemoteCredentials(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"smtp://mailer:s3cret@smtp.example.com:587",
		"smtp://mailer:s3cret@10.0.0.5:25",
		"smtps://mailer:s3cret@smtp.example.com:465",
	} {
		_, err := loadMail(production(map[string]string{"FORGE_MAIL_URL": rawURL, "FORGE_MAIL_FROM": "a@example.com", "FORGE_MAIL_ALLOW_PLAINTEXT": "true"}))
		if err == nil {
			t.Errorf("%s accepted", rawURL)
			continue
		}
		if strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "credentials") {
			t.Errorf("error for %s = %v", rawURL, err)
		}
	}
	allowed := []map[string]string{
		// no credentials: only the message travels in clear, on a trusted network
		{"FORGE_MAIL_URL": "smtp://relay.internal:25", "FORGE_MAIL_ALLOW_PLAINTEXT": "true"},
		// loopback relay with credentials
		{"FORGE_MAIL_URL": "smtp://u:p@localhost:1025", "FORGE_MAIL_ALLOW_PLAINTEXT": "true"},
		{"FORGE_MAIL_URL": "smtp://u:p@127.0.0.1:1025", "FORGE_MAIL_ALLOW_PLAINTEXT": "true"},
		{"FORGE_MAIL_URL": "smtp://u:p@[::1]:1025", "FORGE_MAIL_ALLOW_PLAINTEXT": "true"},
		// credentials over verified TLS without the override
		{"FORGE_MAIL_URL": "smtp://u:p@smtp.example.com:587"},
	}
	for _, env := range allowed {
		env["FORGE_MAIL_FROM"] = "a@example.com"
		if _, err := loadMail(production(env)); err != nil {
			t.Errorf("%v rejected: %v", env, err)
		}
	}
	// Outside production the combination loads (it is refused when sending).
	if _, err := loadMail(map[string]string{"FORGE_MAIL_URL": "smtp://u:p@smtp.example.com:587", "FORGE_MAIL_FROM": "a@example.com", "FORGE_MAIL_ALLOW_PLAINTEXT": "true"}); err != nil {
		t.Errorf("development rejected: %v", err)
	}
}
