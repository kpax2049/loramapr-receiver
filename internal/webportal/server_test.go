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
	codes           []string
	resetCalls      int
	lastDeauthorize bool
	err             error
}

func (r *recordingPairingSubmitter) SubmitPairingCode(_ context.Context, code string) error {
	if r.err != nil {
		return r.err
	}
	r.codes = append(r.codes, code)
	return nil
}

func (r *recordingPairingSubmitter) ResetPairing(_ context.Context, deauthorize bool) error {
	if r.err != nil {
		return r.err
	}
	r.resetCalls++
	r.lastDeauthorize = deauthorize
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

func TestOpsAPI(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ops", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "\"overall\"") {
		t.Fatalf("expected operational summary payload")
	}
	if !strings.Contains(body, "\"attention\"") {
		t.Fatalf("expected attention payload")
	}
	if !strings.Contains(body, "pairing_authorized") {
		t.Fatalf("expected operational checks list")
	}
}

func TestResetRoute(t *testing.T) {
	t.Parallel()

	submitter := &recordingPairingSubmitter{}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/reset", strings.NewReader("deauthorize=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}
	if submitter.resetCalls != 1 {
		t.Fatalf("expected reset call")
	}
	if !submitter.lastDeauthorize {
		t.Fatalf("expected deauthorize=true")
	}
}

func TestLifecycleResetAPI(t *testing.T) {
	t.Parallel()

	submitter := &recordingPairingSubmitter{}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/lifecycle/reset", strings.NewReader(`{"deauthorize":false}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	if submitter.resetCalls != 1 {
		t.Fatalf("expected reset call")
	}
	if submitter.lastDeauthorize {
		t.Fatalf("expected deauthorize=false")
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

func TestProgressPageShowsUpdateStatus(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.UpdateStatus = "outdated"
	snap.UpdateSummary = "Receiver is behind recommended release"
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/progress", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "outdated") {
		t.Fatalf("expected update status on progress page")
	}
}

func TestWelcomePageShowsDerivedAttention(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.FailureCode = "cloud_unreachable"
	snap.FailureSummary = "Cloud endpoint is currently unreachable"
	snap.FailureHint = "Check internet connectivity."

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "action_required") {
		t.Fatalf("expected derived attention state in welcome page")
	}
	if !strings.Contains(body, "cloud_unreachable") {
		t.Fatalf("expected failure/attention code in welcome page")
	}
}

func TestProgressPageShowsAttentionFields(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.AttentionState = "urgent"
	snap.AttentionCategory = "lifecycle"
	snap.AttentionCode = "receiver_replaced"
	snap.AttentionSummary = "Receiver identity was replaced."
	snap.AttentionHint = "Reset pairing and link this installation again."

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/progress", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "receiver_replaced") {
		t.Fatalf("expected explicit attention code")
	}
	if !strings.Contains(body, "Receiver identity was replaced.") {
		t.Fatalf("expected explicit attention summary")
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

func TestTroubleshootingShowsFailureHint(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.FailureCode = "cloud_unreachable"
	snap.FailureSummary = "Cloud endpoint is currently unreachable"
	snap.FailureHint = "Check DNS and outbound network connectivity."

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/troubleshooting", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cloud_unreachable") {
		t.Fatalf("expected failure code in troubleshooting page")
	}
	if !strings.Contains(body, "Check DNS and outbound network connectivity.") {
		t.Fatalf("expected failure hint in troubleshooting page")
	}
}

func TestTroubleshootingLifecycleResetHint(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.FailureCode = "receiver_credential_revoked"
	snap.FailureSummary = "Receiver credential was revoked by cloud"
	snap.FailureHint = "Reset local receiver credentials and re-pair from LoRaMapr Cloud."

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/troubleshooting", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Reset And Re-pair") {
		t.Fatalf("expected lifecycle reset action in troubleshooting page")
	}
}

func TestTroubleshootingUnsupportedVersionHint(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.FailureCode = "receiver_version_unsupported"
	snap.FailureSummary = "Installed receiver version is no longer supported"
	snap.FailureHint = "Upgrade receiver using the supported package or appliance release path."
	snap.UpdateStatus = "unsupported"

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/troubleshooting", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "unsupported") {
		t.Fatalf("expected unsupported guidance in troubleshooting page")
	}
}

func TestTroubleshootingApplianceDiscoveryHints(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.RuntimeProfile = "appliance-pi"
	snap.Components["network"] = status.ComponentStatus{
		State:     "unavailable",
		Message:   "network unavailable",
		UpdatedAt: time.Now().UTC(),
	}

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/troubleshooting", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "loramapr-receiver.local:8080") {
		t.Fatalf("expected appliance local discovery hint")
	}
	if !strings.Contains(body, "Network is unavailable") {
		t.Fatalf("expected appliance network hint")
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
