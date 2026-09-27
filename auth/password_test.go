package auth

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple 🔐")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, "correct horse") || !strings.HasPrefix(hash, "$pbkdf2-sha256$v=1$i=600000$") {
		t.Fatalf("unexpected password hash %q", hash)
	}
	match, rehash, err := VerifyPassword(hash, "correct horse battery staple 🔐")
	if err != nil || !match || rehash {
		t.Fatalf("VerifyPassword()=(%v, %v, %v)", match, rehash, err)
	}
	match, _, err = VerifyPassword(hash, "wrong password")
	if err != nil || match {
		t.Fatalf("wrong password match=%v err=%v", match, err)
	}
}

func TestPasswordHashMarksOlderWorkFactorForUpgrade(t *testing.T) {
	hash, err := hashPassword("secret", minAcceptedPasswordIterations, bytes.NewReader(make([]byte, passwordSaltBytes)))
	if err != nil {
		t.Fatal(err)
	}
	match, rehash, err := VerifyPassword(hash, "secret")
	if err != nil || !match || !rehash {
		t.Fatalf("VerifyPassword()=(%v, %v, %v)", match, rehash, err)
	}
}

func TestPasswordHashRejectsInvalidInputs(t *testing.T) {
	if _, err := HashPassword(""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("empty password error=%v", err)
	}
	if _, err := HashPassword(strings.Repeat("x", maxPasswordBytes+1)); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("long password error=%v", err)
	}
	if _, err := hashPassword("secret", DefaultPasswordIterations, bytes.NewReader(nil)); err == nil {
		t.Fatal("expected entropy failure")
	}
	for _, encoded := range []string{
		"",
		"$argon2id$v=1$i=600000$AA$BB",
		"$pbkdf2-sha256$v=2$i=600000$AA$BB",
		"$pbkdf2-sha256$v=1$i=+600000$AA$BB",
		"$pbkdf2-sha256$v=1$i=99999$AA$BB",
		"$pbkdf2-sha256$v=1$i=600000$not-base64$also-not-base64",
	} {
		if _, _, err := VerifyPassword(encoded, "secret"); !errors.Is(err, ErrPasswordHashInvalid) {
			t.Fatalf("VerifyPassword(%q) error=%v", encoded, err)
		}
	}
}

func FuzzParsePasswordHashNeverPanics(f *testing.F) {
	for _, seed := range []string{"", "$", "$pbkdf2-sha256$", "$pbkdf2-sha256$v=1$i=600000$"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, encoded string) {
		_, _ = parsePasswordHash(encoded)
	})
}
