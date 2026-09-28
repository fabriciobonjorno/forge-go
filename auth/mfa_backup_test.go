package auth

import (
	"strings"
	"testing"
)

func TestMFABackupCodesAreUniqueParseableAndRedacted(t *testing.T) {
	codes, digests, err := newMFABackupCodes(DefaultMFABackupCodeCount)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != DefaultMFABackupCodeCount || len(digests) != DefaultMFABackupCodeCount {
		t.Fatalf("codes=%d digests=%d", len(codes), len(digests))
	}
	seen := make(map[Digest]bool, len(digests))
	for index, code := range codes {
		if code.String() != "[REDACTED]" {
			t.Fatalf("code %d does not redact: %s", index, code)
		}
		plain := code.Reveal()
		if len(plain) != 29 || strings.Count(plain, "-") != 5 {
			t.Fatalf("code %d format=%q", index, plain)
		}
		digest, ok := parseMFABackupCode("  " + strings.ToLower(plain) + "  ")
		if !ok || digest != digests[index] {
			t.Fatalf("code %d did not round trip", index)
		}
		if seen[digest] {
			t.Fatalf("duplicate digest at index %d", index)
		}
		seen[digest] = true
	}
}

func TestParseMFABackupCodeRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{
		"",
		"ABCD",
		"ABCD-EFGH-0JKL-MNOP-QRST-UVWX",
		"AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-extra",
		"!!!!-!!!!-!!!!-!!!!-!!!!-!!!!",
	} {
		if _, ok := parseMFABackupCode(value); ok {
			t.Fatalf("accepted malformed backup code %q", value)
		}
	}
}

func FuzzParseMFABackupCodeNeverPanics(f *testing.F) {
	for _, seed := range []string{"", "ABCD-EFGH-IJKL-MNOP-QRST-UVWX", strings.Repeat("A", 24)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		_, _ = parseMFABackupCode(value)
	})
}
