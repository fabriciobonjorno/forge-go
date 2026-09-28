package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/fabriciobonjorno/forge-go/uuid"
)

const (
	defaultSessionTTL = 24 * time.Hour
	minSessionTTL     = 5 * time.Minute
	maxSessionTTL     = 30 * 24 * time.Hour

	// A syntactically valid hash with an impossible all-zero derived key.
	// Missing identities still execute PBKDF2 so account/tenant misses do not
	// take a dramatically cheaper path than a wrong password.
	dummyPasswordHash = "$pbkdf2-sha256$v=1$i=600000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

var tenantSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// PasswordIdentity is the minimum server-side state needed to verify a login.
// PasswordHash must never be returned to clients or logs.
type PasswordIdentity struct {
	SubjectID         uuid.UUID
	MembershipID      uuid.UUID
	PasswordHash      string
	CredentialVersion int64
	MFARequired       bool
}

// PasswordStore loads current credential state and conditionally upgrades a
// password hash. ReplacePasswordHash must compare previousHash atomically and
// return replaced=false if the credential changed concurrently.
type PasswordStore interface {
	LookupPassword(ctx context.Context, emailNormalized, tenantSlug string) (identity PasswordIdentity, found bool, err error)
	ReplacePasswordHash(ctx context.Context, subjectID uuid.UUID, previousHash, nextHash string) (replaced bool, err error)
}

// SessionCreator persists a new session while revalidating the membership and
// account state. The returned Token is the one-time plaintext credential.
type SessionCreator interface {
	CreateSession(ctx context.Context, membershipID uuid.UUID, expiresAt time.Time) (Token, error)
}

// CredentialSessionCreator creates a session only if credentialVersion is still
// current. Password login and MFA use this to close the race between verifying
// credentials and persisting the resulting session.
type CredentialSessionCreator interface {
	CreateSessionAtVersion(ctx context.Context, membershipID uuid.UUID, credentialVersion int64, expiresAt time.Time) (Token, error)
}

// SessionRevoker invalidates one opaque session credential.
type SessionRevoker interface {
	RevokeSession(ctx context.Context, token Token) error
}

type LoginInput struct {
	Email      string
	Password   string
	TenantSlug string
	Source     string
}

type LoginResult struct {
	Token        Token
	ExpiresAt    time.Time
	MFARequired  bool
	MFAChallenge MFAChallengeToken
}

type LoginService struct {
	passwords PasswordStore
	sessions  CredentialSessionCreator
	throttler LoginThrottler
	mfa       MFAChallengeIssuer
	now       func() time.Time
	ttl       time.Duration
}

type LoginOption func(*LoginService) error

func WithSessionTTL(ttl time.Duration) LoginOption {
	return func(service *LoginService) error {
		if ttl < minSessionTTL || ttl > maxSessionTTL {
			return errors.New("session TTL must be between 5 minutes and 30 days")
		}
		service.ttl = ttl
		return nil
	}
}

func WithLoginClock(now func() time.Time) LoginOption {
	return func(service *LoginService) error {
		if now == nil {
			return errors.New("login clock is required")
		}
		service.now = now
		return nil
	}
}

func WithLoginThrottler(throttler LoginThrottler) LoginOption {
	return func(service *LoginService) error {
		if throttler == nil {
			return errors.New("login throttler is required")
		}
		service.throttler = throttler
		return nil
	}
}

func WithMFAChallengeIssuer(issuer MFAChallengeIssuer) LoginOption {
	return func(service *LoginService) error {
		if issuer == nil {
			return errors.New("MFA challenge issuer is required")
		}
		service.mfa = issuer
		return nil
	}
}

func NewLoginService(passwords PasswordStore, sessions CredentialSessionCreator, options ...LoginOption) (*LoginService, error) {
	if passwords == nil || sessions == nil {
		return nil, errors.New("password store and session creator are required")
	}
	throttler, err := NewMemoryLoginThrottler(DefaultLoginThrottleConfig())
	if err != nil {
		return nil, err
	}
	service := &LoginService{
		passwords: passwords,
		sessions:  sessions,
		throttler: throttler,
		now:       time.Now,
		ttl:       defaultSessionTTL,
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("nil login option")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// Login verifies an active identity in an explicitly named tenant and creates
// an opaque session. All authentication misses return ErrCredentialsInvalid;
// infrastructure and corrupted persisted hashes remain server-side failures.
func (s *LoginService) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	email, tenant, ok := normalizeLogin(input.Email, input.TenantSlug)
	if !ok || input.Password == "" || len(input.Password) > maxPasswordBytes {
		return LoginResult{}, ErrCredentialsInvalid
	}

	throttleKey := deriveLoginThrottleKey(email, tenant, strings.TrimSpace(input.Source))
	decision, err := s.throttler.Attempt(ctx, throttleKey)
	if err != nil {
		return LoginResult{}, err
	}
	if !decision.Allowed {
		return LoginResult{}, &LoginThrottledError{RetryAfter: decision.RetryAfter}
	}

	identity, found, err := s.passwords.LookupPassword(ctx, email, tenant)
	if err != nil {
		return LoginResult{}, err
	}
	if !found {
		_, _, verifyErr := VerifyPassword(dummyPasswordHash, input.Password)
		if verifyErr != nil {
			return LoginResult{}, verifyErr
		}
		return LoginResult{}, ErrCredentialsInvalid
	}
	if identity.SubjectID.Version() != 7 || identity.SubjectID.Variant() != 2 ||
		identity.MembershipID.Version() != 7 || identity.MembershipID.Variant() != 2 ||
		identity.CredentialVersion < 1 {
		return LoginResult{}, errors.New("password store returned an invalid identity")
	}

	match, needsRehash, err := VerifyPassword(identity.PasswordHash, input.Password)
	if err != nil {
		return LoginResult{}, err
	}
	if !match {
		return LoginResult{}, ErrCredentialsInvalid
	}
	if needsRehash {
		nextHash, err := HashPassword(input.Password)
		if err != nil {
			return LoginResult{}, err
		}
		replaced, err := s.passwords.ReplacePasswordHash(ctx, identity.SubjectID, identity.PasswordHash, nextHash)
		if err != nil {
			return LoginResult{}, err
		}
		if !replaced {
			// A concurrent password reset or credential update won the race.
			// Do not issue a session from the stale credential we verified.
			return LoginResult{}, ErrCredentialsInvalid
		}
	}

	if err := s.throttler.Success(ctx, throttleKey); err != nil {
		return LoginResult{}, err
	}
	expiresAt := s.now().UTC().Add(s.ttl)
	if identity.MFARequired {
		if s.mfa == nil {
			return LoginResult{}, errors.New("MFA is required but no challenge issuer is configured")
		}
		challenge, challengeExpiresAt, err := s.mfa.IssueMFAChallenge(ctx, identity.MembershipID, identity.CredentialVersion, expiresAt)
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{
			MFARequired:  true,
			MFAChallenge: challenge,
			ExpiresAt:    challengeExpiresAt,
		}, nil
	}
	token, err := s.sessions.CreateSessionAtVersion(ctx, identity.MembershipID, identity.CredentialVersion, expiresAt)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, ExpiresAt: expiresAt}, nil
}

func normalizeLogin(email, tenant string) (string, string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	tenant = strings.ToLower(strings.TrimSpace(tenant))
	hasUnsafeRune := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
	if len(email) == 0 || len(email) > 320 || strings.Count(email, "@") != 1 || strings.ContainsFunc(email, hasUnsafeRune) {
		return "", "", false
	}
	local, domain, _ := strings.Cut(email, "@")
	if local == "" || domain == "" || !tenantSlugPattern.MatchString(tenant) {
		return "", "", false
	}
	return email, tenant, true
}
