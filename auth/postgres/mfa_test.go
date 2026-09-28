package postgres_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fabriciobonjorno/forge-go/auth"
	authpostgres "github.com/fabriciobonjorno/forge-go/auth/postgres"
	"github.com/fabriciobonjorno/forge-go/postgres/postgrestest"
	"github.com/fabriciobonjorno/forge-go/uuid"
)

func TestTOTPLoginFlowAgainstPostgres(t *testing.T) {
	db := postgrestest.NewMigrated(t, authpostgres.Migrations())
	repo, err := authpostgres.New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	userID, orgID, tenantID, membershipID := uuid.MustNew(), uuid.MustNew(), uuid.MustNew(), uuid.MustNew()
	passwordHash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO forge_users (id, email, email_normalized, password_hash) VALUES ($1, $2, $3, $4)", userID, "Alice@example.com", "alice@example.com", passwordHash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO forge_organizations (id, name) VALUES ($1, 'Acme')", orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO forge_tenants (id, organization_id, slug) VALUES ($1, $2, 'acme')", tenantID, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO forge_memberships (id, user_id, tenant_id) VALUES ($1, $2, $3)", membershipID, userID, tenantID); err != nil {
		t.Fatal(err)
	}

	oldSession, err := repo.CreateSession(ctx, membershipID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	secretCipher, err := auth.NewAESGCMSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	mfa, err := auth.NewMFAService(repo, repo, secretCipher, auth.WithMFAClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := mfa.BeginTOTPEnrollment(ctx, userID, "Forge", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	rawSecret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	code := testTOTPCode(rawSecret, now)
	if err := mfa.ConfirmTOTPEnrollment(ctx, userID, code); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.ResolveSession(ctx, oldSession.Digest()); err != nil || found {
		t.Fatalf("pre-MFA session found=%v err=%v", found, err)
	}

	now = now.Add(30 * time.Second)
	login, err := auth.NewLoginService(
		repo,
		repo,
		auth.WithLoginClock(clock),
		auth.WithMFAChallengeIssuer(mfa),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstFactor, err := login.Login(ctx, auth.LoginInput{
		Email:      "alice@example.com",
		Password:   "correct-password",
		TenantSlug: "acme",
		Source:     "192.0.2.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !firstFactor.MFARequired || firstFactor.MFAChallenge.Reveal() == "" {
		t.Fatalf("first factor result=%+v", firstFactor)
	}

	code = testTOTPCode(rawSecret, now)
	completed, err := mfa.CompleteLogin(ctx, auth.MFACompletion{
		ChallengeToken: firstFactor.MFAChallenge.Reveal(),
		Code:           code,
		Source:         "192.0.2.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.ResolveSession(ctx, completed.Token.Digest()); err != nil || !found {
		t.Fatalf("MFA session found=%v err=%v", found, err)
	}
	if _, err := mfa.CompleteLogin(ctx, auth.MFACompletion{
		ChallengeToken: firstFactor.MFAChallenge.Reveal(),
		Code:           code,
		Source:         "192.0.2.10",
	}); !errors.Is(err, auth.ErrMFAInvalid) {
		t.Fatalf("replayed challenge error=%v", err)
	}
}

func TestMFAChallengeDiesAfterCredentialVersionChange(t *testing.T) {
	db := postgrestest.NewMigrated(t, authpostgres.Migrations())
	repo, err := authpostgres.New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	userID, orgID, tenantID, membershipID := uuid.MustNew(), uuid.MustNew(), uuid.MustNew(), uuid.MustNew()
	passwordHash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO forge_users (id, email, email_normalized, password_hash) VALUES ($1, 'bob@example.com', 'bob@example.com', $2)", userID, passwordHash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO forge_organizations (id, name) VALUES ($1, 'Example')", orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO forge_tenants (id, organization_id, slug) VALUES ($1, $2, 'example')", tenantID, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO forge_memberships (id, user_id, tenant_id) VALUES ($1, $2, $3)", membershipID, userID, tenantID); err != nil {
		t.Fatal(err)
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	secretCipher, err := auth.NewAESGCMSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	mfa, err := auth.NewMFAService(repo, repo, secretCipher, auth.WithMFAClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := mfa.BeginTOTPEnrollment(ctx, userID, "Forge", "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	rawSecret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	if err := mfa.ConfirmTOTPEnrollment(ctx, userID, testTOTPCode(rawSecret, now)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	challenge, _, err := mfa.IssueMFAChallenge(ctx, membershipID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeUserSessions(ctx, userID); err != nil {
		t.Fatal(err)
	}
	_, err = mfa.CompleteLogin(ctx, auth.MFACompletion{
		ChallengeToken: challenge.Reveal(),
		Code:           testTOTPCode(rawSecret, now),
	})
	if !errors.Is(err, auth.ErrMFAInvalid) {
		t.Fatalf("credential-version invalidation error=%v", err)
	}
}

func testTOTPCode(secret []byte, now time.Time) string {
	// RFC 6238 uses HOTP(counter = UnixTime / period). Keep this tiny helper
	// independent of auth internals to exercise the public MFA path.
	counter := uint64(now.Unix() / 30)
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (((uint32(sum[offset]) & 0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])) % 1_000_000
	return fmt.Sprintf("%06d", value)
}
