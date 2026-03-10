package webportal

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/status"
)

type staticStatusProvider struct {
	snapshot status.Snapshot
}

func (s staticStatusProvider) CurrentStatus() status.Snapshot {
	return s.snapshot
}

type recordingPairingSubmitter struct {
	codes []string
	err   error
}

func (r *recordingPairingSubmitter) SubmitPairingCode(_ context.Context, code string) error {
	if r.err != nil {
		return r.err
	}
	r.codes = append(r.codes, code)
	return nil
}

func TestWelcomePage(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "LoRaMapr Receiver Setup Portal") {
		t.Fatalf("expected welcome page content")
	}
}

func TestPairingFormSubmission(t *testing.T) {
	t.Parallel()

	submitter := &recordingPairingSubmitter{}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pairing", strings.NewReader("pairing_code=LMR-ABC12345"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}
	if len(submitter.codes) != 1 || submitter.codes[0] != "LMR-ABC12345" {
		t.Fatalf("expected pairing code submission")
	}
}

func TestPairingAPI(t *testing.T) {
	t.Parallel()

	submitter := &recordingPairingSubmitter{}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/code", strings.NewReader(`{"pairingCode":"LMR-XYZ99999"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	if len(submitter.codes) != 1 || submitter.codes[0] != "LMR-XYZ99999" {
		t.Fatalf("expected pairing code submission")
	}
	if !strings.Contains(rec.Body.String(), `"accepted":true`) {
		t.Fatalf("expected accepted response body")
	}
}

func TestProgressPage(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.PairingPhase = "bootstrap_exchanged"
	snap.CloudStatus = "activating"
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/progress", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "bootstrap_exchanged") {
		t.Fatalf("expected pairing phase in progress page")
	}
	if !strings.Contains(body, "activating") {
		t.Fatalf("expected cloud state in progress page")
	}
}

func TestAdvancedPage(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/advanced", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "Advanced Details") {
		t.Fatalf("expected advanced page heading")
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, &recordingPairingSubmitter{}, nil)
}

func sampleSnapshot() status.Snapshot {
	now := time.Date(2026, 3, 10, 20, 0, 0, 0, time.UTC)
	return status.Snapshot{
		InstallationID: "install-1",
		Mode:           "setup",
		RuntimeProfile: "local-dev",
		Lifecycle:      status.LifecycleRunning,
		PairingPhase:   "unpaired",
		CloudEndpoint:  "https://api.example.com",
		CloudStatus:    "unknown",
		Ready:          true,
		ReadyReason:    "setup portal available",
		StartedAt:      now,
		UpdatedAt:      now,
		Components: map[string]status.ComponentStatus{
			"meshtastic": {
				State:     "not_present",
				UpdatedAt: now,
			},
		},
	}
}
