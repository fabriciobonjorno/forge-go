package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryLoginThrottlerLimitsAccountAndResetsOnSuccess(t *testing.T) {
	now := time.Date(2026, 9, 27, 13, 0, 0, 0, time.UTC)
	limiter, err := newMemoryLoginThrottler(LoginThrottleConfig{
		Window:       time.Minute,
		AccountLimit: 2,
		SourceLimit:  10,
		MaxEntries:   100,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	key := deriveLoginThrottleKey("alice@example.com", "acme", "192.0.2.10")
	for i := 0; i < 2; i++ {
		decision, err := limiter.Attempt(context.Background(), key)
		if err != nil || !decision.Allowed {
			t.Fatalf("attempt %d decision=%+v err=%v", i+1, decision, err)
		}
	}
	decision, err := limiter.Attempt(context.Background(), key)
	if err != nil || decision.Allowed || decision.RetryAfter != time.Minute {
		t.Fatalf("throttled decision=%+v err=%v", decision, err)
	}
	if err := limiter.Success(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	decision, err = limiter.Attempt(context.Background(), key)
	if err != nil || !decision.Allowed {
		t.Fatalf("after success decision=%+v err=%v", decision, err)
	}
}

func TestMemoryLoginThrottlerLimitsSourceAcrossAccounts(t *testing.T) {
	limiter, err := newMemoryLoginThrottler(LoginThrottleConfig{
		Window:       time.Minute,
		AccountLimit: 10,
		SourceLimit:  2,
		MaxEntries:   100,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	keys := []LoginThrottleKey{
		deriveLoginThrottleKey("a@example.com", "acme", "192.0.2.10"),
		deriveLoginThrottleKey("b@example.com", "acme", "192.0.2.10"),
		deriveLoginThrottleKey("c@example.com", "acme", "192.0.2.10"),
	}
	for i := 0; i < 2; i++ {
		if decision, err := limiter.Attempt(context.Background(), keys[i]); err != nil || !decision.Allowed {
			t.Fatalf("attempt %d decision=%+v err=%v", i+1, decision, err)
		}
	}
	if decision, err := limiter.Attempt(context.Background(), keys[2]); err != nil || decision.Allowed || decision.RetryAfter <= 0 {
		t.Fatalf("source limit decision=%+v err=%v", decision, err)
	}
}

func TestLoginServiceThrottlesBeforeCredentialLookup(t *testing.T) {
	limiter, err := newMemoryLoginThrottler(LoginThrottleConfig{
		Window:       time.Minute,
		AccountLimit: 1,
		SourceLimit:  10,
		MaxEntries:   100,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	store := &loginStoreStub{}
	service, err := NewLoginService(store, &sessionStub{}, WithLoginThrottler(limiter))
	if err != nil {
		t.Fatal(err)
	}
	input := LoginInput{Email: "a@example.com", Password: "wrong", TenantSlug: "acme", Source: "192.0.2.10"}
	if _, err := service.Login(context.Background(), input); !errors.Is(err, ErrCredentialsInvalid) {
		t.Fatalf("first error=%v", err)
	}
	store.lookupErr = errors.New("lookup should not run again")
	_, err = service.Login(context.Background(), input)
	var throttled *LoginThrottledError
	if !errors.As(err, &throttled) || !errors.Is(err, ErrLoginThrottled) || throttled.RetryAfter <= 0 {
		t.Fatalf("second error=%v", err)
	}
}
