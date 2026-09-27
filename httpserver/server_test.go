package httpserver_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fabriciobonjorno/forge-go/config"
	"github.com/fabriciobonjorno/forge-go/httpserver"
	"github.com/fabriciobonjorno/forge-go/web"
)

func TestServerMiddleware(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		handler    http.Handler
		body       string
		wantStatus int
	}{
		{name: "success", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), wantStatus: http.StatusNoContent},
		{name: "oversized body", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), body: "12345", wantStatus: http.StatusRequestEntityTooLarge},
		{name: "panic", handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("sensitive panic") }), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.HTTP.MaxBodyBytes = 4
			server, err := httpserver.New(cfg, test.handler, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d", recorder.Code, test.wantStatus)
			}
			// X-Frame-Options and CSP are checked because http.Error sets nosniff on its own.
			if recorder.Header().Get("X-Request-ID") == "" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" ||
				recorder.Header().Get("X-Frame-Options") != "DENY" || recorder.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing secure response headers")
			}
			if strings.Contains(recorder.Body.String(), "sensitive panic") {
				t.Fatal("panic leaked to client")
			}
		})
	}
}

func TestRequestContextCarriesRequestID(t *testing.T) {
	t.Parallel()
	var seen string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = web.RequestID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	server, err := httpserver.New(config.Default(), handler, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || seen != recorder.Header().Get("X-Request-ID") {
		t.Fatalf("context request ID %q does not match header %q", seen, recorder.Header().Get("X-Request-ID"))
	}
}
