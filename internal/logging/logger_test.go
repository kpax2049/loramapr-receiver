package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestJSONLoggerIncludesRequiredFields(t *testing.T) {
	t.Setenv("LORAMAPR_ENVIRONMENT", "test")

	var out bytes.Buffer
	handler, err := newHandler(&out, "json", slog.LevelInfo)
	if err != nil {
		t.Fatalf("newHandler returned error: %v", err)
	}
	logger := slog.New(handler)

	logger.Info(
		"heartbeat sent",
		"component", "heartbeat.send",
		"request_id", "req-123",
		"receiver_id", "rx-123",
		"status_code", 201,
		"error_code", "",
	)

	entry := decodeJSONLog(t, out.Bytes())
	if entry["timestamp"] == nil {
		t.Fatalf("expected timestamp field")
	}
	if entry["level"] != "INFO" {
		t.Fatalf("expected level INFO, got %#v", entry["level"])
	}
	if entry["service"] != serviceName {
		t.Fatalf("expected service %q, got %#v", serviceName, entry["service"])
	}
	if entry["environment"] != "test" {
		t.Fatalf("expected environment test, got %#v", entry["environment"])
	}
	if entry["message"] != "heartbeat sent" {
		t.Fatalf("expected message field, got %#v", entry["message"])
	}
	if entry["requestId"] != "req-123" {
		t.Fatalf("expected requestId req-123, got %#v", entry["requestId"])
	}
	if entry["receiverId"] != "rx-123" {
		t.Fatalf("expected receiverId rx-123, got %#v", entry["receiverId"])
	}
	if entry["operation"] != "heartbeat.send" {
		t.Fatalf("expected operation heartbeat.send, got %#v", entry["operation"])
	}
	if entry["statusCode"] != float64(201) {
		t.Fatalf("expected statusCode 201, got %#v", entry["statusCode"])
	}
	if _, ok := entry["errorCode"]; !ok {
		t.Fatalf("expected errorCode field")
	}
}

func TestJSONLoggerAddsMissingRequiredFields(t *testing.T) {
	t.Setenv("LORAMAPR_ENVIRONMENT", "local-dev")

	var out bytes.Buffer
	handler, err := newHandler(&out, "json", slog.LevelInfo)
	if err != nil {
		t.Fatalf("newHandler returned error: %v", err)
	}
	logger := slog.New(handler)
	logger.Info("runtime started")

	entry := decodeJSONLog(t, out.Bytes())
	if entry["requestId"] != "" {
		t.Fatalf("expected empty requestId default, got %#v", entry["requestId"])
	}
	if entry["receiverId"] != "" {
		t.Fatalf("expected empty receiverId default, got %#v", entry["receiverId"])
	}
	if entry["operation"] != "" {
		t.Fatalf("expected empty operation default, got %#v", entry["operation"])
	}
	if entry["statusCode"] != float64(0) {
		t.Fatalf("expected default statusCode 0, got %#v", entry["statusCode"])
	}
	if entry["errorCode"] != "" {
		t.Fatalf("expected empty errorCode default, got %#v", entry["errorCode"])
	}
}

func decodeJSONLog(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(bytes.TrimSpace(payload), &out); err != nil {
		t.Fatalf("decode log json: %v", err)
	}
	return out
}
