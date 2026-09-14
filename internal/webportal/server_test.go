package webportal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshcore"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

type staticStatusProvider struct {
	snapshot status.Snapshot
}

func (s staticStatusProvider) CurrentStatus() status.Snapshot {
	return s.snapshot
}

type recordingPairingSubmitter struct {
	codes                []string
	resetCalls           int
	lastDeauthorize      bool
	err                  error
	collisionResolutions []string

	homeCfg               config.HomeAutoSessionConfig
	homeSaveCalls         int
	homeReevaluateCalls   int
	homeResetStateCalls   int
	lastSavedHomeMode     config.HomeAutoSessionMode
	lastSavedTrackedNodes []string
}

type blePairingSubmitter struct {
	*recordingPairingSubmitter
	devices []meshcore.BLEDevice
	pairCfg meshcore.BLEConfig
	pairPIN string
}

type telemetrySubmitter struct {
	*recordingPairingSubmitter
	result meshcore.TelemetryResult
	err    error
	key    string
}

type trackingSubmitter struct {
	*recordingPairingSubmitter
	status meshcore.TrackingStatus
	key    string
	err    error
}

type lifecycleSubmitter struct {
	*recordingPairingSubmitter
	status       meshcore.AdapterStatus
	releaseCalls int
	resumeCalls  int
	err          error
}

func (s *lifecycleSubmitter) ReleaseMeshCoreBLE(context.Context) (meshcore.AdapterStatus, error) {
	s.releaseCalls++
	return s.status, s.err
}

func (s *lifecycleSubmitter) ResumeMeshCoreBLE(context.Context) (meshcore.AdapterStatus, error) {
	s.resumeCalls++
	return s.status, s.err
}

func (s *telemetrySubmitter) RequestMeshCoreTelemetry(_ context.Context, key string) (meshcore.TelemetryResult, error) {
	s.key = key
	return s.result, s.err
}

func (s *trackingSubmitter) StartMeshCoreTracking(_ context.Context, key string) (meshcore.TrackingStatus, error) {
	s.key = key
	return s.status, s.err
}
func (s *trackingSubmitter) StopMeshCoreTracking(context.Context) (meshcore.TrackingStatus, error) {
	return s.status, s.err
}
func (s *trackingSubmitter) MeshCoreTrackingStatus(context.Context) (meshcore.TrackingStatus, error) {
	return s.status, s.err
}

func (s *blePairingSubmitter) DiscoverMeshCoreBLE(_ context.Context, _ string) ([]meshcore.BLEDevice, error) {
	return append([]meshcore.BLEDevice(nil), s.devices...), nil
}
func (s *blePairingSubmitter) PairMeshCoreBLE(_ context.Context, cfg meshcore.BLEConfig, pin string) error {
	s.pairCfg, s.pairPIN = cfg, pin
	return nil
}
func (s *blePairingSubmitter) ForgetMeshCoreBLE(_ context.Context, cfg meshcore.BLEConfig) error {
	s.pairCfg = cfg
	return nil
}

func TestMeshCoreBLEPairingAPIIsLocalAndDoesNotEchoPIN(t *testing.T) {
	t.Parallel()
	submitter := &blePairingSubmitter{recordingPairingSubmitter: &recordingPairingSubmitter{}, devices: []meshcore.BLEDevice{{Address: "AA:BB:CC:DD:EE:FF", Name: "MeshCore", Bonded: true}}}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)
	devices := httptest.NewRecorder()
	srv.Handler().ServeHTTP(devices, httptest.NewRequest(http.MethodGet, "/api/meshcore/ble/devices?adapter=hci0", nil))
	if devices.Code != http.StatusOK || !strings.Contains(devices.Body.String(), "AA:BB:CC:DD:EE:FF") {
		t.Fatalf("devices status=%d body=%s", devices.Code, devices.Body.String())
	}
	pair := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/meshcore/ble/pair", strings.NewReader(`{"adapter":"hci0","peerAddress":"AA:BB:CC:DD:EE:FF","pin":"123456"}`))
	srv.Handler().ServeHTTP(pair, req)
	if pair.Code != http.StatusAccepted || strings.Contains(pair.Body.String(), "123456") || submitter.pairPIN != "123456" || submitter.pairCfg.PeerAddress != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("pair status=%d body=%s cfg=%#v", pair.Code, pair.Body.String(), submitter.pairCfg)
	}
}

func TestMeshCoreTelemetryRequestAPIValidatesAndReturnsLocalResult(t *testing.T) {
	t.Parallel()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	voltage := 4.09
	submitter := &telemetrySubmitter{
		recordingPairingSubmitter: &recordingPairingSubmitter{},
		result: meshcore.TelemetryResult{
			TargetPublicKey: key, SourcePrefix: key[:12], ReceivedAt: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
			Telemetry: meshcore.Telemetry{Voltage: &voltage},
		},
	}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/meshcore/telemetry/request", strings.NewReader(`{"publicKey":"`+key+`"}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || submitter.key != key {
		t.Fatalf("request status=%d body=%s key=%q", rec.Code, rec.Body.String(), submitter.key)
	}
	var result meshcore.TelemetryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.SourcePrefix != key[:12] || result.Telemetry.Voltage == nil || *result.Telemetry.Voltage != voltage {
		t.Fatalf("unexpected result=%#v err=%v", result, err)
	}

	submitter.err = meshcore.ErrTelemetryTimeout
	timeout := httptest.NewRecorder()
	srv.Handler().ServeHTTP(timeout, httptest.NewRequest(http.MethodPost, "/api/meshcore/telemetry/request", strings.NewReader(`{"publicKey":"`+key+`"}`)))
	if timeout.Code != http.StatusGatewayTimeout || !strings.Contains(timeout.Body.String(), `"outcome":"timeout"`) {
		t.Fatalf("timeout status=%d body=%s", timeout.Code, timeout.Body.String())
	}

	submitter.err = meshcore.ErrInvalidTelemetryTarget
	invalid := httptest.NewRecorder()
	srv.Handler().ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/meshcore/telemetry/request", strings.NewReader(`{"publicKey":"short"}`)))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"outcome":"invalid_target"`) {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}

	submitter.err = meshcore.ErrTelemetryAdapterDisconnected
	released := httptest.NewRecorder()
	srv.Handler().ServeHTTP(released, httptest.NewRequest(http.MethodPost, "/api/meshcore/telemetry/request", strings.NewReader(`{"publicKey":"`+key+`"}`)))
	if released.Code != http.StatusServiceUnavailable || !strings.Contains(released.Body.String(), `"outcome":"disconnected_adapter"`) {
		t.Fatalf("released status=%d body=%s", released.Code, released.Body.String())
	}

}

func TestMeshCoreTrackingAPIsAreExplicitLocalLifecycleHarness(t *testing.T) {
	t.Parallel()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	submitter := &trackingSubmitter{recordingPairingSubmitter: &recordingPairingSubmitter{}, status: meshcore.TrackingStatus{Active: true, TargetPublicKey: key, MotionState: meshcore.MotionUnknown, CurrentIntervalSeconds: 30}}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)
	start := httptest.NewRecorder()
	srv.Handler().ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/api/meshcore/tracking/start", strings.NewReader(`{"publicKey":"`+key+`"}`)))
	if start.Code != http.StatusAccepted || submitter.key != key || !strings.Contains(start.Body.String(), `"active":true`) {
		t.Fatalf("start status=%d body=%s key=%q", start.Code, start.Body.String(), submitter.key)
	}
	status := httptest.NewRecorder()
	srv.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/meshcore/tracking/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"targetPublicKey":"`+key+`"`) {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
	stop := httptest.NewRecorder()
	srv.Handler().ServeHTTP(stop, httptest.NewRequest(http.MethodPost, "/api/meshcore/tracking/stop", nil))
	if stop.Code != http.StatusOK {
		t.Fatalf("stop status=%d body=%s", stop.Code, stop.Body.String())
	}
	submitter.err = meshcore.ErrTrackingDifferentTarget
	conflict := httptest.NewRecorder()
	srv.Handler().ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/api/meshcore/tracking/start", strings.NewReader(`{"publicKey":"`+key+`"}`)))
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"outcome":"tracking_active"`) {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestMeshCoreBLELifecycleAPIsAreLocalAndIdempotent(t *testing.T) {
	t.Parallel()
	submitter := &lifecycleSubmitter{recordingPairingSubmitter: &recordingPairingSubmitter{}, status: meshcore.AdapterStatus{
		State: meshcore.StateReleased, Transport: "ble", Configured: "AA:BB:CC:DD:EE:FF", ReconnectSuppressed: true, ReleasedByUser: true,
	}}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)
	for range 2 {
		release := httptest.NewRecorder()
		srv.Handler().ServeHTTP(release, httptest.NewRequest(http.MethodPost, "/api/meshcore/adapter/release", nil))
		if release.Code != http.StatusOK || !strings.Contains(release.Body.String(), `"ReconnectSuppressed":true`) || !strings.Contains(release.Body.String(), `"ReleasedByUser":true`) {
			t.Fatalf("release status=%d body=%s", release.Code, release.Body.String())
		}
	}
	if submitter.releaseCalls != 2 {
		t.Fatalf("release calls=%d", submitter.releaseCalls)
	}
	submitter.status = meshcore.AdapterStatus{State: meshcore.StateNotPresent, Transport: "ble", Configured: "AA:BB:CC:DD:EE:FF"}
	for range 2 {
		resume := httptest.NewRecorder()
		srv.Handler().ServeHTTP(resume, httptest.NewRequest(http.MethodPost, "/api/meshcore/adapter/resume", nil))
		if resume.Code != http.StatusOK || strings.Contains(resume.Body.String(), `"ReleasedByUser":true`) {
			t.Fatalf("resume status=%d body=%s", resume.Code, resume.Body.String())
		}
	}
	if submitter.resumeCalls != 2 {
		t.Fatalf("resume calls=%d", submitter.resumeCalls)
	}
}

func TestMeshCoreDashboardRendersOperatorSurfaces(t *testing.T) {
	t.Parallel()
	snap := sampleSnapshot()
	snap.ReceiverVersion = "v3.4.0"
	snap.Adapters = []status.AdapterStatus{{
		Name: "meshcore-companion", Protocol: "meshcore", Lifecycle: "connected", ConnectionState: "connected",
		Enabled: true, Configured: true, Ready: true, Transport: "ble", ConfiguredDevice: "AA:BB:CC:DD:EE:FF", ConnectedDevice: "AA:BB:CC:DD:EE:FF",
	}}
	tracking := &trackingSubmitter{recordingPairingSubmitter: &recordingPairingSubmitter{}, status: meshcore.TrackingStatus{
		Active: true, TargetPublicKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", MotionState: meshcore.MotionSlow,
		CurrentIntervalSeconds: 30, RecentPolls: []meshcore.TrackingPoll{{
			Outcome: "success", MotionState: meshcore.MotionSlow, IntervalSeconds: 30,
			RouteAttempt: meshcore.RouteEvidence{Mode: meshcore.RouteModeExplicitPath, Path: []string{"18"}, PathLength: 1}, ResponseRouteUnknown: true,
		}},
	}}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, tracking, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/meshcore", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, expected := range []string{
		"Receiver / MeshCore", "MeshCore connection", "Release device", "Resume receiver connection",
		"Adaptive tracking", "Latest telemetry", "Recent telemetry polls", "not a trusted signed current position",
		"not repeater identities", "meshcore-dashboard.js",
	} {
		if !strings.Contains(rec.Body.String(), expected) {
			t.Fatalf("dashboard missing %q: %s", expected, rec.Body.String())
		}
	}

	js := httptest.NewRecorder()
	srv.Handler().ServeHTTP(js, httptest.NewRequest(http.MethodGet, "/static/js/meshcore-dashboard.js", nil))
	for _, expected := range []string{
		"Connected", "Connecting / reconnecting", "Released for external use", "Bluetooth unavailable", "Direct / 0-hop", "Explicit path",
		"Raw path hashes", "response route unknown", "setHidden(byID(\"meshcore-release-note\"), !released)", "updateError(actionableError(adapter && adapter.last_error)",
	} {
		if !strings.Contains(js.Body.String(), expected) {
			t.Fatalf("dashboard script missing %q", expected)
		}
	}
}

func TestMeshCoreDashboardLifecycleFailureReturnsActionableError(t *testing.T) {
	t.Parallel()
	submitter := &lifecycleSubmitter{recordingPairingSubmitter: &recordingPairingSubmitter{}, err: context.DeadlineExceeded}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)
	for _, action := range []string{"release", "resume"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/meshcore/adapter/"+action, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"context deadline exceeded"`) {
			t.Fatalf("action=%s status=%d body=%s", action, rec.Code, rec.Body.String())
		}
	}
}

func TestMeshCoreDashboardRendersConnectionAndTrackingStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, label string
		adapter     status.AdapterStatus
	}{
		{name: "connected", label: "Connected", adapter: status.AdapterStatus{Protocol: "meshcore", ConnectionState: "connected", Ready: true, Transport: "ble"}},
		{name: "reconnecting", label: "Connecting / reconnecting", adapter: status.AdapterStatus{Protocol: "meshcore", ConnectionState: "connecting", Transport: "ble"}},
		{name: "released", label: "Released for external use", adapter: status.AdapterStatus{Protocol: "meshcore", ConnectionState: "released", Transport: "ble", ReleasedByUser: true, ReconnectSuppressed: true}},
		{name: "bluetooth unavailable", label: "Bluetooth unavailable", adapter: status.AdapterStatus{Protocol: "meshcore", Lifecycle: "not_present", ConnectionState: "disconnected", Transport: "ble"}},
		{name: "error", label: "Error", adapter: status.AdapterStatus{Protocol: "meshcore", Lifecycle: "configuration_error", ConnectionState: "error", Transport: "ble", LastError: "BlueZ transport failed"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			snap := sampleSnapshot()
			snap.Adapters = []status.AdapterStatus{test.adapter}
			srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &trackingSubmitter{recordingPairingSubmitter: &recordingPairingSubmitter{}}, nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/meshcore", nil))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), test.label) {
				t.Fatalf("label=%q status=%d body=%s", test.label, rec.Code, rec.Body.String())
			}
		})
	}
	for _, active := range []bool{true, false} {
		t.Run("tracking_"+map[bool]string{true: "active", false: "inactive"}[active], func(t *testing.T) {
			srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, &trackingSubmitter{recordingPairingSubmitter: &recordingPairingSubmitter{}, status: meshcore.TrackingStatus{Active: active}}, nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/meshcore", nil))
			want := "Inactive"
			if active {
				want = "Active"
			}
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("active=%v status=%d body=%s", active, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMeshCoreDashboardStateDependentAlertsAndActions(t *testing.T) {
	newPage := func(adapter status.AdapterStatus) string {
		t.Helper()
		snap := sampleSnapshot()
		snap.Adapters = []status.AdapterStatus{adapter}
		srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &trackingSubmitter{recordingPairingSubmitter: &recordingPairingSubmitter{}}, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/meshcore", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("dashboard status=%d body=%s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	const releaseButton = `id="meshcore-release" class="lmr-btn lmr-btn--warn" type="button"`
	const resumeButton = `id="meshcore-resume" class="lmr-btn" type="button"`
	const releasedNotice = `id="meshcore-release-note" class="lmr-callout is-info"`

	t.Run("connected has no released or empty error alert", func(t *testing.T) {
		body := newPage(status.AdapterStatus{Protocol: "meshcore", ConnectionState: "connected", Ready: true, Transport: "ble", ConfiguredDevice: "AA:BB", ConnectedDevice: "AA:BB", ReleasedByUser: true, ReconnectSuppressed: true})
		if !strings.Contains(body, `>Connected</span>`) || !strings.Contains(body, releaseButton+`>Release device</button>`) {
			t.Fatalf("connected action/state missing: %s", body)
		}
		if !strings.Contains(body, resumeButton+` hidden>`) || !strings.Contains(body, releasedNotice+` hidden>`) {
			t.Fatalf("connected state did not hide released controls: %s", body)
		}
		if strings.Contains(body, `id="meshcore-error"`) {
			t.Fatalf("connected state rendered an empty error alert: %s", body)
		}
		if !strings.Contains(body, `id="meshcore-reconnect" class="lmr-mono">enabled`) {
			t.Fatalf("connected state did not retain enabled reconnect wording: %s", body)
		}
	})

	t.Run("released shows notice and resume without error styling", func(t *testing.T) {
		body := newPage(status.AdapterStatus{Protocol: "meshcore", ConnectionState: "released", Transport: "ble", ReleasedByUser: true, ReconnectSuppressed: true})
		if !strings.Contains(body, `>Released for external use</span>`) || !strings.Contains(body, releasedNotice+`><span`) || !strings.Contains(body, resumeButton+`>Resume receiver connection</button>`) {
			t.Fatalf("released state missing notice/resume: %s", body)
		}
		if !strings.Contains(body, releaseButton+` hidden>`) || strings.Contains(body, `id="meshcore-error"`) {
			t.Fatalf("released state rendered release or error alert: %s", body)
		}
	})

	t.Run("reconnecting does not render released messaging", func(t *testing.T) {
		body := newPage(status.AdapterStatus{Protocol: "meshcore", ConnectionState: "connecting", Transport: "ble"})
		if !strings.Contains(body, `>Connecting / reconnecting</span>`) || !strings.Contains(body, releasedNotice+` hidden>`) || !strings.Contains(body, releaseButton+` hidden>`) {
			t.Fatalf("reconnecting state rendered invalid controls: %s", body)
		}
	})

	t.Run("actionable error renders and cleared error is absent", func(t *testing.T) {
		body := newPage(status.AdapterStatus{Protocol: "meshcore", Lifecycle: "configuration_error", ConnectionState: "error", Transport: "ble", LastError: "BlueZ connection failed"})
		if !strings.Contains(body, `id="meshcore-error" class="lmr-callout is-fail"`) || !strings.Contains(body, `Adapter error: BlueZ connection failed`) {
			t.Fatalf("actionable error was not rendered: %s", body)
		}
		cleared := newPage(status.AdapterStatus{Protocol: "meshcore", Lifecycle: "configuration_error", ConnectionState: "error", Transport: "ble", LastError: "   "})
		if strings.Contains(cleared, `id="meshcore-error"`) {
			t.Fatalf("cleared error left an alert behind: %s", cleared)
		}
	})
}

func TestPortalEmbeddedAssetsLoadFromUnrelatedWorkingDirectory(t *testing.T) {
	if os.Getenv("LMR_PORTAL_EXTERNAL_CWD_HELPER") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPortalEmbeddedAssetsLoadFromUnrelatedWorkingDirectory$")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "LMR_PORTAL_EXTERNAL_CWD_HELPER=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("portal failed outside the repository cwd: %v\n%s", err, output)
		}
		return
	}

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, &recordingPairingSubmitter{}, nil)
	page := httptest.NewRecorder()
	srv.Handler().ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/meshcore", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Receiver / MeshCore") {
		t.Fatalf("embedded dashboard did not render: status=%d body=%s", page.Code, page.Body.String())
	}
	for _, asset := range []string{"/static/css/tokens.css", "/static/css/portal.css", "/static/js/theme-toggle.js", "/static/js/meshcore-dashboard.js"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, asset, nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Fatalf("embedded asset %s failed outside repository cwd: status=%d", asset, rec.Code)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := srv.Run(ctx); err != nil {
		t.Fatalf("portal startup failed outside repository cwd: %v", err)
	}
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

func (r *recordingPairingSubmitter) ResolveNormalizedDeliveryCollision(_ context.Context, deliveryID string) error {
	if r.err != nil {
		return r.err
	}
	r.collisionResolutions = append(r.collisionResolutions, deliveryID)
	return nil
}

func TestNormalizedCollisionResolutionAPI(t *testing.T) {
	t.Parallel()
	submitter := &recordingPairingSubmitter{}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/normalized-outbox/collision/resolve", strings.NewReader(`{"deliveryId":"0198c7a2-e395-7000-8000-000000000030"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted || len(submitter.collisionResolutions) != 1 || submitter.collisionResolutions[0] != "0198c7a2-e395-7000-8000-000000000030" {
		t.Fatalf("status=%d resolutions=%v body=%s", rec.Code, submitter.collisionResolutions, rec.Body.String())
	}
}

func (r *recordingPairingSubmitter) CurrentHomeAutoSessionConfig() config.HomeAutoSessionConfig {
	if r.homeCfg.Mode == "" {
		r.homeCfg.Mode = config.HomeAutoSessionModeOff
		r.homeCfg.StartDebounce = config.Duration(30 * time.Second)
		r.homeCfg.StopDebounce = config.Duration(30 * time.Second)
		r.homeCfg.IdleStopTimeout = config.Duration(15 * time.Minute)
		r.homeCfg.Cloud.StartEndpoint = "/api/receiver/home-auto-session/start"
		r.homeCfg.Cloud.StopEndpoint = "/api/receiver/home-auto-session/stop"
	}
	return r.homeCfg
}

func (r *recordingPairingSubmitter) UpdateHomeAutoSessionConfig(_ context.Context, cfg config.HomeAutoSessionConfig) error {
	if r.err != nil {
		return r.err
	}
	r.homeSaveCalls++
	r.homeCfg = cfg
	r.lastSavedHomeMode = cfg.Mode
	r.lastSavedTrackedNodes = append([]string(nil), cfg.TrackedNodeIDs...)
	return nil
}

func (r *recordingPairingSubmitter) ReevaluateHomeAutoSession(_ context.Context) error {
	if r.err != nil {
		return r.err
	}
	r.homeReevaluateCalls++
	return nil
}

func (r *recordingPairingSubmitter) ResetHomeAutoSession(_ context.Context) error {
	if r.err != nil {
		return r.err
	}
	r.homeResetStateCalls++
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

func TestStatusEventsAPI(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/events/status", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE handler to return")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("expected event-stream content type, got %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: status") {
		t.Fatalf("expected SSE status event payload")
	}
	if !strings.Contains(body, "\"installation_id\":\"install-1\"") {
		t.Fatalf("expected serialized status snapshot payload")
	}
}

func TestOpsAPIIncludesSetupIssues(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.RuntimeProfile = "linux-service"
	snap.InstallType = "linux-package"
	snap.CloudEndpoint = "https://api.loramapr.example"
	snap.CloudStatus = "unreachable"
	snap.CloudReachable = false
	snap.Components["portal"] = status.ComponentStatus{
		State:   "running",
		Message: "local setup portal listening on 127.0.0.1:8080",
	}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ops", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "\"setup_issues\"") {
		t.Fatalf("expected setup_issues in ops payload")
	}
	if !strings.Contains(body, "portal_bind_localhost") {
		t.Fatalf("expected portal bind setup issue")
	}
	if !strings.Contains(body, "cloud_base_url_placeholder") {
		t.Fatalf("expected cloud placeholder setup issue")
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

func TestProgressPageSubmittedFlashTracksPairingPhase(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.PairingPhase = "steady_state"
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/progress?submitted=1", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Pairing completed.") {
		t.Fatalf("expected completed pairing message after steady_state, got %q", body)
	}
	if strings.Contains(body, "Pairing code submitted. Progress updates will appear here.") {
		t.Fatalf("expected submitted progress message to clear once pairing is complete")
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

func TestWelcomePageShowsSetupRootCausePanel(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.RuntimeProfile = "linux-service"
	snap.InstallType = "linux-package"
	snap.CloudEndpoint = "https://api.loramapr.example"
	snap.CloudStatus = "unreachable"
	snap.CloudReachable = false
	snap.Components["portal"] = status.ComponentStatus{
		State:   "running",
		Message: "local setup portal listening on 127.0.0.1:8080",
	}

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Setup root cause") {
		t.Fatalf("expected setup root cause panel")
	}
	if !strings.Contains(body, "portal_bind_localhost") {
		t.Fatalf("expected portal bind root cause in welcome page")
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

func TestProgressPageShowsReceiverIdentityContext(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.LocalName = "garage-pi-a1b2c3"
	snap.Hostname = "garage-pi"
	snap.CloudReceiverID = "rx-123"
	snap.CloudReceiverLabel = "Garage Receiver"
	snap.CloudSiteLabel = "Home"
	snap.CloudGroupLabel = "Outdoor"

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/progress", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "garage-pi-a1b2c3") {
		t.Fatalf("expected local receiver name in progress page")
	}
	if !strings.Contains(body, "Garage Receiver") {
		t.Fatalf("expected cloud receiver label in progress page")
	}
}

func TestProgressPageShowsSetupRootCause(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.PairingPhase = "steady_state"
	snap.CloudEndpoint = "https://loramapr.com"
	snap.CloudStatus = "reachable"
	snap.CloudReachable = true
	snap.Components["meshtastic"] = status.ComponentStatus{
		State:   "degraded",
		Message: "device=/dev/ttyACM0 error=native serial stream unreadable",
	}

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/progress", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Setup Root Cause") {
		t.Fatalf("expected setup root cause section on progress page")
	}
	if !strings.Contains(body, "usb_protocol_unusable") {
		t.Fatalf("expected protocol root cause code on progress page")
	}
}

func TestProgressPageShowsMeshtasticConfigSummary(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.PairingPhase = "steady_state"
	snap.MeshtasticConfig = status.MeshtasticConfigSnapshot{
		Available:         true,
		Region:            "EU_868",
		PrimaryChannel:    "Home Mesh",
		PSKState:          "present",
		ShareURLAvailable: true,
		ShareURL:          "https://meshtastic.org/e/#CwgB",
		ShareURLRedacted:  "https://meshtastic.org/e/#<redacted>",
		Source:            "status_event",
	}

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/progress", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Field-Node Pairing Data") {
		t.Fatalf("expected field-node pairing section")
	}
	if !strings.Contains(body, "EU_868") || !strings.Contains(body, "Home Mesh") {
		t.Fatalf("expected meshtastic config summary details")
	}
	if !strings.Contains(body, "https://meshtastic.org/e/#CwgB") {
		t.Fatalf("expected share URL in progress page")
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

func TestTroubleshootingUnpairedHintStatesOptionalOrgMetadata(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.PairingPhase = "unpaired"

	hints := strings.Join(troubleshootingHints(snap), "\n")
	if !strings.Contains(hints, "does not require workspace/site/group setup") {
		t.Fatalf("expected explicit non-required org metadata hint, got %q", hints)
	}
}

func TestTroubleshootingShowsMultiReceiverReplacementHint(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.FailureCode = "receiver_replaced"
	snap.FailureSummary = "Receiver was replaced by another installation"
	snap.CloudReceiverLabel = "Backyard Receiver"

	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: snap}, &recordingPairingSubmitter{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/troubleshooting", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "superseded by another receiver") {
		t.Fatalf("expected multi-receiver replacement guidance")
	}
	if strings.Contains(strings.ToLower(body), "household/team") {
		t.Fatalf("did not expect household/team wording in replacement guidance")
	}
}

func TestTroubleshootingUnsupportedVersionHint(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.FailureCode = "receiver_version_unsupported"
	snap.FailureSummary = "Installed receiver version is no longer supported"
	snap.FailureHint = "Upgrade receiver using the supported package release path."
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

func TestTroubleshootingShowsMeshtasticConfigFallbackHint(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.PairingPhase = "steady_state"
	snap.MeshtasticConfig = status.MeshtasticConfigSnapshot{
		Available:         false,
		UnavailableReason: "connected node has not reported channel/config summary",
	}

	hints := strings.Join(troubleshootingHints(snap), "\n")
	if !strings.Contains(hints, "Field-node pairing data is unavailable") {
		t.Fatalf("expected field-node pairing unavailable hint, got %q", hints)
	}
	if !strings.Contains(hints, "Meshtastic app") {
		t.Fatalf("expected Meshtastic app fallback hint, got %q", hints)
	}
}

func TestTroubleshootingShowsSetupRootCauseHints(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.PairingPhase = "steady_state"
	snap.Components["meshtastic"] = status.ComponentStatus{
		State:   "connecting",
		Message: "device=/dev/ttyACM0 packets=0 observed_nodes=0",
	}

	hints := strings.Join(troubleshootingHints(snap), "\n")
	if !strings.Contains(hints, "Setup root cause [usb_detected_node_not_ready]") {
		t.Fatalf("expected setup root cause troubleshooting hint, got %q", hints)
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

func TestHomeAutoSessionPage(t *testing.T) {
	t.Parallel()

	submitter := &recordingPairingSubmitter{}
	submitter.homeCfg = config.HomeAutoSessionConfig{
		Enabled:          true,
		Mode:             config.HomeAutoSessionModeObserve,
		StartDebounce:    config.Duration(30 * time.Second),
		StopDebounce:     config.Duration(30 * time.Second),
		IdleStopTimeout:  config.Duration(15 * time.Minute),
		StartupReconcile: true,
	}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/home-auto-session", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Home Auto Session") {
		t.Fatalf("expected home auto session page content")
	}
	if !strings.Contains(body, "Control State") {
		t.Fatalf("expected control state field in home auto session page")
	}
	if !strings.Contains(body, "Last Action Result") {
		t.Fatalf("expected last action result field in home auto session page")
	}
}

func TestHomeAutoSessionSaveForm(t *testing.T) {
	t.Parallel()

	submitter := &recordingPairingSubmitter{}
	srv := New("127.0.0.1:0", staticStatusProvider{snapshot: sampleSnapshot()}, submitter, nil)

	form := strings.NewReader(strings.Join([]string{
		"enabled=1",
		"mode=observe",
		"home_lat=37.3349",
		"home_lon=-122.0090",
		"home_radius_m=150",
		"tracked_node_ids=!nodeA,!nodeB",
		"start_debounce=30s",
		"stop_debounce=30s",
		"idle_stop_timeout=15m",
		"startup_reconcile=1",
		"session_name_template=Home+Auto+%7B%7B.NodeID%7D%7D",
		"session_notes_template=Generated+by+receiver",
	}, "&"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/home-auto-session", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if submitter.homeSaveCalls != 1 {
		t.Fatalf("expected one home auto save call")
	}
	if submitter.homeReevaluateCalls != 1 {
		t.Fatalf("expected one home auto reevaluate call")
	}
	if submitter.lastSavedHomeMode != config.HomeAutoSessionModeObserve {
		t.Fatalf("unexpected saved home auto mode: %s", submitter.lastSavedHomeMode)
	}
	if len(submitter.lastSavedTrackedNodes) != 2 {
		t.Fatalf("unexpected saved tracked nodes: %#v", submitter.lastSavedTrackedNodes)
	}
}

func TestHomeAutoStateHintLifecycleBlocked(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.HomeAutoSession.Enabled = true
	snap.HomeAutoSession.Mode = "control"
	snap.HomeAutoSession.State = "degraded"
	snap.HomeAutoSession.ControlState = "lifecycle_blocked"
	snap.HomeAutoSession.BlockedReason = "receiver credential revoked by cloud"

	hint := homeAutoStateHint(snap)
	if !strings.Contains(strings.ToLower(hint), "lifecycle-blocked") {
		t.Fatalf("expected lifecycle-blocked hint, got %q", hint)
	}
}

func TestHomeAutoStateHintConflictBlocked(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.HomeAutoSession.Enabled = true
	snap.HomeAutoSession.Mode = "control"
	snap.HomeAutoSession.State = "degraded"
	snap.HomeAutoSession.ControlState = "conflict_blocked"
	snap.HomeAutoSession.BlockedReason = "cloud reports an active session already exists"

	hint := homeAutoStateHint(snap)
	if !strings.Contains(strings.ToLower(hint), "conflict") {
		t.Fatalf("expected conflict hint, got %q", hint)
	}
}

func TestHomeAutoConfigHintCloudManaged(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.HomeAutoSession.EffectiveConfigSource = "cloud_managed"
	snap.HomeAutoSession.EffectiveConfigVer = "has-v2"
	snap.HomeAutoSession.DesiredConfigEnabled = true
	snap.HomeAutoSession.DesiredConfigMode = "control"

	hint := homeAutoConfigHint(snap)
	if !strings.Contains(strings.ToLower(hint), "cloud-managed") {
		t.Fatalf("expected cloud-managed hint, got %q", hint)
	}
	if !strings.Contains(hint, "has-v2") {
		t.Fatalf("expected config version in hint, got %q", hint)
	}
}

func TestHomeAutoConfigHintInvalidCloudFallback(t *testing.T) {
	t.Parallel()

	snap := sampleSnapshot()
	snap.HomeAutoSession.EffectiveConfigSource = "local_fallback"
	snap.HomeAutoSession.LastConfigApplyResult = "cloud_config_invalid_local_fallback"

	hint := homeAutoConfigHint(snap)
	if !strings.Contains(strings.ToLower(hint), "invalid") {
		t.Fatalf("expected invalid-cloud fallback hint, got %q", hint)
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
		LocalName:      "receiver-install-1",
		Hostname:       "receiver-host",
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
