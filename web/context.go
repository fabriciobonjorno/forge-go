// Package web provides the HTTP adapter toolkit for Forge handlers: JSON
// responses, a single error response format, strict request decoding and the
// request-scoped values (request ID, logger) set by the HTTP server.
package web

import (
	"context"
	"io"
	"log/slog"
)

type contextKey int

const (
	requestIDKey contextKey = iota
	loggerKey
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the ID the server assigned to the current request.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// Logger returns the request-scoped logger, which carries the request ID. It
// never returns nil.
func Logger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return discardLogger
}
