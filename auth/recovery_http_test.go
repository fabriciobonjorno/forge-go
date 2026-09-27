package auth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPasswordRecoveryRequestHandlerIsUniform(t *testing.T) {
	service, err := NewRecoveryService(&recoveryStoreStub{}, &recoverySenderStub{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPasswordRecoveryRequestHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"email":"nobody@example.com","tenant":"acme"}`,
		`{"email":"not-email","tenant":"acme"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/auth/recovery", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("body=%s status=%d headers=%v response=%s", body, recorder.Code, recorder.Header(), recorder.Body.String())
		}
	}
}

func TestPasswordRecoveryRequestHandlerReturnsRetryAfter(t *testing.T) {
	limiter, err := newMemoryLoginThrottler(LoginThrottleConfig{
		Window:       time.Minute,
		AccountLimit: 1,
		SourceLimit:  10,
		MaxEntries:   100,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewRecoveryService(&recoveryStoreStub{}, &recoverySenderStub{}, WithRecoveryThrottler(limiter))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPasswordRecoveryRequestHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	call := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/auth/recovery", bytes.NewBufferString(`{"email":"nobody@example.com","tenant":"acme"}`))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "192.0.2.10:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if first := call(); first.Code != http.StatusAccepted {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := call()
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" {
		t.Fatalf("second status=%d retry-after=%q body=%s", second.Code, second.Header().Get("Retry-After"), second.Body.String())
	}
}

func TestPasswordResetHandlerConsumesToken(t *testing.T) {
	token, err := NewRecoveryToken()
	if err != nil {
		t.Fatal(err)
	}
	store := &recoveryStoreStub{consumeResult: true}
	service, err := NewRecoveryService(store, RecoverySenderFunc(func(context.Context, PasswordRecoveryMessage) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPasswordResetHandler(service)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"token":"` + token.Reveal() + `","new_password":"replacement password"}`
	request := httptest.NewRequest(http.MethodPost, "/auth/reset-password", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if store.consumedDigest != token.Digest() || store.consumedHash == "" {
		t.Fatalf("digest=%x hash=%q", store.consumedDigest, store.consumedHash)
	}

	bad := httptest.NewRequest(http.MethodPost, "/auth/reset-password", bytes.NewBufferString(`{"token":"bad","new_password":"replacement password"}`))
	bad.Header.Set("Content-Type", "application/json")
	badRecorder := httptest.NewRecorder()
	handler.ServeHTTP(badRecorder, bad)
	if badRecorder.Code != http.StatusBadRequest {
		t.Fatalf("bad status=%d body=%s", badRecorder.Code, badRecorder.Body.String())
	}
}
