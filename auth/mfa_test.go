package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fabriciobonjorno/forge-go/uuid"
)

type mfaStoreStub struct {
	pending                  TOTPSecretRecord
	pendingFound             bool
	confirmResult            bool
	confirmCounter           int64
	challenge                MFAChallengeRecord
	challengeFound           bool
	consumeResult            bool
	consumeMembership        uuid.UUID
	consumeCounter           int64
	createdChallenge         Digest
	createdMembership        uuid.UUID
	createdCredentialVersion int64
	createdExpiresAt         time.Time
	createdSessionExpiry     time.Time
	err                      error
}

func (s *mfaStoreStub) BeginTOTPEnrollment(_ context.Context, subjectID uuid.UUID, digest Digest, ciphertext []byte, expiresAt time.Time) error {
	if s.err != nil {
		return s.err
	}
	s.pending = TOTPSecretRecord{
		SubjectID:        subjectID,
		SecretDigest:      digest,
		SecretCiphertext: append([]byte(nil), ciphertext...),
		ExpiresAt:        expiresAt,
	}
	s.pendingFound = true
	return nil
}

func (s *mfaStoreStub) LoadPendingTOTP(context.Context, uuid.UUID) (TOTPSecretRecord, bool, error) {
	if s.err != nil {
		return TOTPSecretRecord{}, false, s.err
	}
	return s.pending, s.pendingFound, nil
}

func (s *mfaStoreStub) ConfirmTOTPEnrollment(_ context.Context, _ uuid.UUID, _ Digest, counter int64) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	s.confirmCounter = counter
	return s.confirmResult, nil
}

func (s *mfaStoreStub) CreateMFAChallenge(_ context.Context, digest Digest, membershipID uuid.UUID, credentialVersion int64, expiresAt, sessionExpiresAt time.Time) error {
	if s.err != nil {
		return s.err
	}
	s.createdChallenge = digest
	s.createdMembership = membershipID
	s.createdCredentialVersion = credentialVersion
	s.createdExpiresAt = expiresAt
	s.createdSessionExpiry = sessionExpiresAt
	return nil
}

func (s *mfaStoreStub) LoadMFAChallenge(context.Context, Digest) (MFAChallengeRecord, bool, error) {
	if s.err != nil {
		return MFAChallengeRecord{}, false, s.err
	}
	return s.challenge, s.challengeFound, nil
}

func (s *mfaStoreStub) ConsumeMFAChallenge(_ context.Context, _ Digest, _ Digest, counter int64) (uuid.UUID, bool, error) {
	if s.err != nil {
		return uuid.UUID{}, false, s.err
	}
	s.consumeCounter = counter
	if !s.consumeResult {
		return uuid.UUID{}, false, nil
	}
	return s.consumeMembership, true, nil
}

type mfaIssuerStub struct {
	token             MFAChallengeToken
	expiresAt         time.Time
	membershipID      uuid.UUID
	credentialVersion int64
	sessionExpiresAt  time.Time
	err               error
}

func (s *mfaIssuerStub) IssueMFAChallenge(_ context.Context, membershipID uuid.UUID, credentialVersion int64, sessionExpiresAt time.Time) (MFAChallengeToken, time.Time, error) {
	s.membershipID = membershipID
	s.credentialVersion = credentialVersion
	s.sessionExpiresAt = sessionExpiresAt
	return s.token, s.expiresAt, s.err
}

func TestAESGCMSecretCipherRoundTripAndTamperDetection(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewAESGCMSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("01234567890123456789")
	sealed, err := cipher.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if string(sealed) == string(plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}
	opened, err := cipher.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != string(plaintext) {
		t.Fatalf("opened=%q", opened)
	}
	sealed[len(sealed)-1] ^= 0x01
	if _, err := cipher.Open(sealed); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestTOTPUsesRFC4226HOTPValues(t *testing.T) {
	secret := []byte("12345678901234567890")
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for counter, expected := range want {
		if got := totpCode(secret, uint64(counter)); got != expected {
			t.Fatalf("counter=%d got=%s want=%s", counter, got, expected)
		}
	}
}

func TestTOTPEnrollmentRequiresConfirmationAndPreventsCodeReplay(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secretCipher, err := NewAESGCMSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	store := &mfaStoreStub{confirmResult: true}
	sessions := &sessionStub{}
	now := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	service, err := NewMFAService(store, sessions, secretCipher, WithMFAClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	subjectID := uuid.MustNew()
	enrollment, err := service.BeginTOTPEnrollment(context.Background(), subjectID, "Forge", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.Secret.Reveal() == "" || !strings.HasPrefix(enrollment.ProvisioningURI.Reveal(), "otpauth://totp/") {
		t.Fatalf("enrollment secret=%s uri=%s", enrollment.Secret, enrollment.ProvisioningURI)
	}
	if enrollment.Secret.String() != "[REDACTED]" || enrollment.ProvisioningURI.String() != "[REDACTED]" {
		t.Fatal("TOTP enrollment secrets do not redact")
	}
	rawSecret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	code := totpCode(rawSecret, uint64(now.Unix()/totpPeriodSeconds))
	if err := service.ConfirmTOTPEnrollment(context.Background(), subjectID, code); err != nil {
		t.Fatal(err)
	}
	counter := now.Unix() / totpPeriodSeconds
	if store.confirmCounter != counter {
		t.Fatalf("confirmed counter=%d want=%d", store.confirmCounter, counter)
	}
	if _, ok := matchTOTPCounter(rawSecret, code, now, counter); ok {
		t.Fatal("same TOTP counter was accepted twice")
	}
}

func TestLoginWithMFAIssuesChallengeInsteadOfSession(t *testing.T) {
	hash, err := HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := NewMFAChallengeToken()
	if err != nil {
		t.Fatal(err)
	}
	membershipID := uuid.MustNew()
	store := &loginStoreStub{
		found: true,
		identity: PasswordIdentity{
			SubjectID:    uuid.MustNew(),
			MembershipID: membershipID,
			PasswordHash:      hash,
			CredentialVersion: 1,
			MFARequired:       true,
		},
	}
	sessions := &sessionStub{}
	now := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	issuer := &mfaIssuerStub{token: challenge, expiresAt: now.Add(5 * time.Minute)}
	service, err := NewLoginService(
		store,
		sessions,
		WithLoginClock(func() time.Time { return now }),
		WithMFAChallengeIssuer(issuer),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Login(context.Background(), LoginInput{
		Email: "alice@example.com", Password: "correct-password", TenantSlug: "acme",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.MFARequired || result.MFAChallenge.Digest() != challenge.Digest() {
		t.Fatalf("result=%+v", result)
	}
	if sessions.membership != (uuid.UUID{}) {
		t.Fatal("password login created a session before MFA")
	}
	if issuer.membershipID != membershipID || issuer.credentialVersion != 1 || !issuer.sessionExpiresAt.Equal(now.Add(defaultSessionTTL)) {
		t.Fatalf("issuer membership=%s version=%d session expiry=%v", issuer.membershipID, issuer.credentialVersion, issuer.sessionExpiresAt)
	}
}

func TestMFACompletionConsumesChallengeBeforeCreatingSession(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secretCipher, err := NewAESGCMSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("12345678901234567890")
	sealed, err := secretCipher.Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(secret)
	challengeToken, err := NewMFAChallengeToken()
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	membershipID := uuid.MustNew()
	store := &mfaStoreStub{
		challengeFound: true,
		challenge: MFAChallengeRecord{
			SubjectID:        uuid.MustNew(),
			MembershipID:      membershipID,
			CredentialVersion: 1,
			SecretDigest:      digest,
			SecretCiphertext:  sealed,
			LastCounter:       -1,
			ExpiresAt:         now.Add(5 * time.Minute),
			SessionExpiresAt:  now.Add(12 * time.Hour),
		},
		consumeResult:     true,
		consumeMembership: membershipID,
	}
	sessions := &sessionStub{token: sessionToken}
	service, err := NewMFAService(store, sessions, secretCipher, WithMFAClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	code := totpCode(secret, uint64(now.Unix()/totpPeriodSeconds))
	result, err := service.CompleteLogin(context.Background(), MFACompletion{
		ChallengeToken: challengeToken.Reveal(),
		Code:           code,
		Source:         "192.0.2.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.consumeCounter != now.Unix()/totpPeriodSeconds {
		t.Fatalf("consume counter=%d", store.consumeCounter)
	}
	if sessions.membership != membershipID || sessions.credentialVersion != 1 || !sessions.expiresAt.Equal(now.Add(12*time.Hour)) {
		t.Fatalf("session membership=%s version=%d expires=%v", sessions.membership, sessions.credentialVersion, sessions.expiresAt)
	}
	if result.Token.Digest() != sessionToken.Digest() || !result.ExpiresAt.Equal(now.Add(12*time.Hour)) {
		t.Fatalf("result=%+v", result)
	}
}

func TestMFACompletionFailsClosedOnConsumeRace(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secretCipher, err := NewAESGCMSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("12345678901234567890")
	sealed, err := secretCipher.Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(secret)
	challengeToken, _ := NewMFAChallengeToken()
	now := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	store := &mfaStoreStub{
		challengeFound: true,
		challenge: MFAChallengeRecord{
			SubjectID:        uuid.MustNew(),
			MembershipID:      uuid.MustNew(),
			CredentialVersion: 1,
			SecretDigest:      digest,
			SecretCiphertext:  sealed,
			LastCounter:       -1,
			ExpiresAt:         now.Add(5 * time.Minute),
			SessionExpiresAt:  now.Add(time.Hour),
		},
		consumeResult: false,
	}
	sessions := &sessionStub{}
	service, err := NewMFAService(store, sessions, secretCipher, WithMFAClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	code := totpCode(secret, uint64(now.Unix()/totpPeriodSeconds))
	_, err = service.CompleteLogin(context.Background(), MFACompletion{ChallengeToken: challengeToken.Reveal(), Code: code})
	if !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("error=%v", err)
	}
	if sessions.membership != (uuid.UUID{}) {
		t.Fatal("session created after MFA consume race")
	}
}

func FuzzParseMFAChallengeTokenNeverPanics(f *testing.F) {
	for _, seed := range []string{"", "x", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, secret string) {
		_, _ = ParseMFAChallengeToken(secret)
	})
}
