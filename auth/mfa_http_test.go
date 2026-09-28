package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fabriciobonjorno/forge-go/tenancy"
	"github.com/fabriciobonjorno/forge-go/uuid"
)

func TestLoginHandlerReturnsMFAChallengeWithoutSession(t *testing.T) {
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
			SubjectID:         uuid.MustNew(),
			MembershipID:      membershipID,
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
	handler, err := NewLoginHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"email":"alice@example.com","password":"correct-password","tenant":"acme"}`)
	request := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response mfaRequiredResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.MFARequired || response.ChallengeToken != challenge.Reveal() || !response.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("response=%+v", response)
	}
	if sessions.membership != (uuid.UUID{}) {
		t.Fatal("login HTTP created a session before MFA")
	}
}

func TestMFACompletionHandlerReturnsSession(t *testing.T) {
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
	challenge, err := NewMFAChallengeToken()
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
			SecretDigest:      sha256.Sum256(secret),
			SecretCiphertext:  sealed,
			LastCounter:       -1,
			ExpiresAt:         now.Add(5 * time.Minute),
			SessionExpiresAt:  now.Add(time.Hour),
		},
		consumeResult:     true,
		consumeMembership: membershipID,
	}
	sessions := &sessionStub{token: sessionToken}
	service, err := NewMFAService(store, sessions, secretCipher, WithMFAClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewMFACompletionHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	code := totpCode(secret, uint64(now.Unix()/totpPeriodSeconds))
	body, err := json.Marshal(mfaCompletionRequest{ChallengeToken: challenge.Reveal(), Code: code})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/mfa", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "192.0.2.10:4242"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", recorder.Header().Get("Cache-Control"))
	}
	var response loginResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AccessToken != sessionToken.Reveal() || !response.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("response=%+v", response)
	}
}

func TestTOTPEnrollmentHandlerRequiresExplicitStepUp(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secretCipher, err := NewAESGCMSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewMFAService(&mfaStoreStub{}, &sessionStub{}, secretCipher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTOTPEnrollmentHandler(service, "Forge", nil); err == nil {
		t.Fatal("nil enrollment authorizer was accepted")
	}

	tenant, err := tenancy.New(uuid.MustNew())
	if err != nil {
		t.Fatal(err)
	}
	principal, err := NewPrincipal(uuid.MustNew(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	authorized := false
	handler, err := NewTOTPEnrollmentHandler(service, "Forge", MFAEnrollmentAuthorizerFunc(func(_ context.Context, got Principal) error {
		authorized = true
		if got.SubjectID() != principal.SubjectID() {
			t.Fatalf("subject=%s", got.SubjectID())
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/mfa/totp/enroll", nil)
	request = request.WithContext(withPrincipal(request.Context(), principal))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !authorized {
		t.Fatalf("status=%d authorized=%v body=%s", recorder.Code, authorized, recorder.Body.String())
	}
	var response totpEnrollmentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Secret == "" || response.ProvisioningURI == "" {
		t.Fatalf("response=%+v", response)
	}
}
