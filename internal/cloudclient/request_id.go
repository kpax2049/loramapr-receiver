package cloudclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type requestIDContextKey struct{}

// WithRequestID stores request ID correlation metadata in context for outbound calls.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	value := strings.TrimSpace(requestID)
	if value == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey{}, value)
}

// RequestIDFromContext returns an existing request ID or empty string.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return strings.TrimSpace(value)
}

// EnsureRequestID reuses an existing request ID or generates one if missing.
func EnsureRequestID(ctx context.Context) (context.Context, string) {
	if existing := RequestIDFromContext(ctx); existing != "" {
		return ctx, existing
	}
	requestID := generateRequestID()
	return WithRequestID(ctx, requestID), requestID
}

func generateRequestID() string {
	var entropy [12]byte
	if _, err := rand.Read(entropy[:]); err == nil {
		return "req-" + hex.EncodeToString(entropy[:])
	}
	return fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
}
