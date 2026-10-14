package config_test

import "testing"

func TestMailPreviewFlag(t *testing.T) {
	t.Parallel()
	cfg, err := loadMail(map[string]string{"FORGE_MAIL_PREVIEW": "true"})
	if err != nil || !cfg.Mail.Preview {
		t.Fatalf("development: %+v %v", cfg.Mail, err)
	}
	if cfg, err = loadMail(map[string]string{"FORGE_ENV": "test", "FORGE_MAIL_PREVIEW": "1"}); err != nil || !cfg.Mail.Preview {
		t.Fatalf("test: %+v %v", cfg.Mail, err)
	}
	if cfg, err = loadMail(map[string]string{}); err != nil || cfg.Mail.Preview {
		t.Fatalf("default must be off: %+v %v", cfg.Mail, err)
	}
	for _, env := range []map[string]string{
		production(map[string]string{"FORGE_MAIL_PREVIEW": "true"}),
		{"FORGE_ENV": "staging", "FORGE_MAIL_PREVIEW": "true"},
	} {
		if _, err := loadMail(env); err == nil {
			t.Errorf("%v accepted", env)
		}
	}
	if _, err := loadMail(production(map[string]string{"FORGE_MAIL_PREVIEW": "false"})); err != nil {
		t.Errorf("explicit false rejected: %v", err)
	}
	if _, err := loadMail(map[string]string{"FORGE_MAIL_PREVIEW": "maybe"}); err == nil {
		t.Error("malformed flag accepted")
	}
}
