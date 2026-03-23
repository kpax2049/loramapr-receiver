package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/loramapr/loramapr-receiver/internal/config"
)

const (
	serviceName = "loramapr-receiver"
)

func New(cfg config.LoggingConfig) (*slog.Logger, error) {
	return NewWithWriter(cfg, os.Stdout)
}

func NewWithWriter(cfg config.LoggingConfig, writer io.Writer) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	handler, err := newHandler(writer, cfg.Format, level)
	if err != nil {
		return nil, err
	}
	return slog.New(handler), nil
}

func newHandler(w io.Writer, format string, level slog.Level) (slog.Handler, error) {
	opts := &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	}

	var base slog.Handler
	switch strings.ToLower(format) {
	case "json":
		base = slog.NewJSONHandler(w, opts)
	case "text":
		base = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}

	return &requiredFieldsHandler{
		next:        base,
		service:     serviceName,
		environment: resolveEnvironment(),
	}, nil
}

func replaceAttr(_ []string, attr slog.Attr) slog.Attr {
	switch attr.Key {
	case slog.TimeKey:
		attr.Key = "timestamp"
	case slog.MessageKey:
		attr.Key = "message"
	case "component":
		attr.Key = "operation"
	case "request_id":
		attr.Key = "requestId"
	case "receiver_id":
		attr.Key = "receiverId"
	case "status_code":
		attr.Key = "statusCode"
	case "error_code":
		attr.Key = "errorCode"
	}
	return attr
}

func resolveEnvironment() string {
	for _, key := range []string{
		"LORAMAPR_ENVIRONMENT",
		"LORAMAPR_ENV",
		"APP_ENV",
		"ENVIRONMENT",
	} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "unknown"
}

type requiredFieldsHandler struct {
	next        slog.Handler
	service     string
	environment string
}

func (h *requiredFieldsHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *requiredFieldsHandler) Handle(ctx context.Context, rec slog.Record) error {
	keys := map[string]struct{}{}
	rec.Attrs(func(attr slog.Attr) bool {
		keys[requiredFieldKey(attr.Key)] = struct{}{}
		return true
	})

	addIfMissing := func(key string, attr slog.Attr) {
		if _, ok := keys[key]; ok {
			return
		}
		rec.AddAttrs(attr)
		keys[key] = struct{}{}
	}

	addIfMissing("service", slog.String("service", h.service))
	addIfMissing("environment", slog.String("environment", h.environment))
	addIfMissing("requestId", slog.String("requestId", ""))
	addIfMissing("receiverId", slog.String("receiverId", ""))
	addIfMissing("operation", slog.String("operation", ""))
	addIfMissing("statusCode", slog.Int("statusCode", 0))
	addIfMissing("errorCode", slog.String("errorCode", ""))

	return h.next.Handle(ctx, rec)
}

func requiredFieldKey(key string) string {
	switch key {
	case "component":
		return "operation"
	case "request_id":
		return "requestId"
	case "receiver_id":
		return "receiverId"
	case "status_code":
		return "statusCode"
	case "error_code":
		return "errorCode"
	default:
		return key
	}
}

func (h *requiredFieldsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &requiredFieldsHandler{
		next:        h.next.WithAttrs(attrs),
		service:     h.service,
		environment: h.environment,
	}
}

func (h *requiredFieldsHandler) WithGroup(name string) slog.Handler {
	return &requiredFieldsHandler{
		next:        h.next.WithGroup(name),
		service:     h.service,
		environment: h.environment,
	}
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", value)
	}
}
