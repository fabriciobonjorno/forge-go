package auth_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fabriciobonjorno/forge-go/auth"
	"github.com/fabriciobonjorno/forge-go/tenancy"
	"github.com/fabriciobonjorno/forge-go/uuid"
)

func TestTokenRoundTripAndEntropyFailure(t *testing.T) {
	t.Parallel()
	token, err := auth.NewTokenWithEntropy(bytes.NewReader(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := auth.ParseToken(token.Reveal())
	if err != nil || parsed.Digest() != token.Digest() {
		t.Fatalf("ParseToken()=(%v, %v)", parsed, err)
	}
	if _, err := auth.ParseToken(token.Reveal() + "="); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("non-canonical token error=%v", err)
	}
	if _, err := auth.NewTokenWithEntropy(bytes.NewReader(nil)); err == nil {
		t.Fatal("expected entropy failure")
	}
	if formatted := fmt.Sprintf("%v %#v", token, token); formatted != "[REDACTED] auth.Token([REDACTED])" {
		t.Fatalf("token formatting is not redacted: %q", formatted)
	}
}

func TestMiddlewareAuthenticatesCurrentSessionAndTenant(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	token, principal := fixture(t)
	resolver := auth.ResolverFunc(func(_ context.Context, digest auth.Digest) (auth.Session, bool, error) {
		if digest != token.Digest() {
			return auth.Session{}, false, nil
		}
		return auth.Session{Principal: principal, ExpiresAt: now.Add(time.Hour)}, true, nil
	})
	middleware, err := auth.NewMiddleware(resolver, auth.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := auth.FromContext(r.Context())
		tenant, tenantErr := tenancy.Require(r.Context())
		if !ok || tenantErr != nil || got.SubjectID() != principal.SubjectID() || tenant != principal.Tenant() {
			t.Errorf("principal=%v ok=%v tenant=%v err=%v", got, ok, tenant, tenantErr)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	request.Header.Set("Authorization", "Bearer "+token.Reveal())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMiddlewareRejectsCredentialsFailClosed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	token, principal := fixture(t)
	resolver := auth.ResolverFunc(func(_ context.Context, _ auth.Digest) (auth.Session, bool, error) {
		return auth.Session{Principal: principal, ExpiresAt: now}, true, nil
	})
	middleware, err := auth.NewMiddleware(resolver, auth.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		headers []string
	}{
		{name: "missing"},
		{name: "wrong scheme", headers: []string{"Basic " + token.Reveal()}},
		{name: "duplicate", headers: []string{"Bearer " + token.Reveal(), "Bearer " + token.Reveal()}},
		{name: "combined", headers: []string{"Bearer " + token.Reveal() + ", Bearer " + token.Reveal()}},
		{name: "expired", headers: []string{"Bearer " + token.Reveal()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/private", nil)
			for _, value := range test.headers {
				request.Header.Add("Authorization", value)
			}
			recorder := httptest.NewRecorder()
			middleware.Authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler called") })).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("WWW-Authenticate") == "" {
				t.Fatalf("status=%d authenticate=%q body=%s", recorder.Code, recorder.Header().Get("WWW-Authenticate"), recorder.Body.String())
			}
		})
	}
}

func TestMiddlewarePreservesResolverFailure(t *testing.T) {
	t.Parallel()
	token, _ := fixture(t)
	resolver := auth.ResolverFunc(func(context.Context, auth.Digest) (auth.Session, bool, error) {
		return auth.Session{}, false, errors.New("database unavailable")
	})
	middleware, err := auth.NewMiddleware(resolver)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	request.Header.Set("Authorization", "Bearer "+token.Reveal())
	recorder := httptest.NewRecorder()
	middleware.Authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler called") })).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRequirePermission(t *testing.T) {
	t.Parallel()
	token, principal := fixture(t)
	resolver := auth.ResolverFunc(func(context.Context, auth.Digest) (auth.Session, bool, error) {
		return auth.Session{Principal: principal, ExpiresAt: time.Now().Add(time.Hour)}, true, nil
	})
	middleware, err := auth.NewMiddleware(resolver)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := auth.Require("tasks:read", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	if err != nil {
		t.Fatal(err)
	}
	denied, err := auth.Require("tasks:write", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	if err != nil {
		t.Fatal(err)
	}
	for name, handler := range map[string]http.Handler{"allowed": allowed, "denied": denied} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/tasks", nil)
			request.Header.Set("Authorization", "Bearer "+token.Reveal())
			recorder := httptest.NewRecorder()
			middleware.Authenticate(handler).ServeHTTP(recorder, request)
			want := http.StatusNoContent
			if name == "denied" {
				want = http.StatusForbidden
			}
			if recorder.Code != want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, want, recorder.Body.String())
			}
		})
	}
}

func fixture(t *testing.T) (auth.Token, auth.Principal) {
	t.Helper()
	token, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := tenancy.New(uuid.MustNew())
	if err != nil {
		t.Fatal(err)
	}
	permission, err := auth.NewPermission("tasks:read")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(uuid.MustNew(), tenant, permission)
	if err != nil {
		t.Fatal(err)
	}
	return token, principal
}
