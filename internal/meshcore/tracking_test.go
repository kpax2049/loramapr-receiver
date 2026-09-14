package meshcore

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const trackingKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestDefaultTrackingPolicyIntervals(t *testing.T) {
	policy := DefaultTrackingPolicy()
	if policy.UnknownInterval != 30*time.Second || policy.StationaryInterval != 45*time.Second || policy.SlowInterval != 30*time.Second || policy.FastInterval != 15*time.Second || policy.MinimumInterval != 10*time.Second {
		t.Fatalf("unexpected default policy: %#v", policy)
	}
}

func TestTrackingStartValidatesTargetAndIssuesFirstRequest(t *testing.T) {
	requested := make(chan string, 1)
	release := make(chan struct{})
	controller := NewTrackingController(func(_ context.Context, key string) (TelemetryResult, error) {
		requested <- key
		<-release
		return TelemetryResult{}, ErrTelemetryAdapterDisconnected
	}, DefaultTrackingPolicy(), nil)
	if _, err := controller.Start("short"); !errors.Is(err, ErrInvalidTelemetryTarget) {
		t.Fatalf("invalid target error=%v", err)
	}
	status, err := controller.Start(trackingKey)
	if err != nil || !status.Active || status.TargetPublicKey != trackingKey {
		t.Fatalf("start status=%#v err=%v", status, err)
	}
	select {
	case key := <-requested:
		if key != trackingKey {
			t.Fatalf("requested key=%q", key)
		}
	case <-time.After(time.Second):
		t.Fatal("first request was not issued")
	}
	if _, err := controller.Start(trackingKey); !errors.Is(err, ErrTrackingAlreadyActive) {
		t.Fatalf("same target error=%v", err)
	}
	if _, err := controller.Start("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"); !errors.Is(err, ErrTrackingDifferentTarget) {
		t.Fatalf("different target error=%v", err)
	}
	controller.Stop()
	close(release)
}

func TestTrackingStopPreventsFuturePollsWithoutCancelingInFlightResponse(t *testing.T) {
	started, release, finished := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
	var requests atomic.Int32
	controller := NewTrackingController(func(context.Context, string) (TelemetryResult, error) {
		requests.Add(1)
		started <- struct{}{}
		<-release
		finished <- struct{}{}
		return telemetryFix(52, 13, time.Now().UTC()), nil
	}, DefaultTrackingPolicy(), nil)
	if _, err := controller.Start(trackingKey); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("poll did not start")
	}
	if status := controller.Stop(); status.Active {
		t.Fatalf("stop status=%#v", status)
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("in-flight request was canceled")
	}
	select {
	case <-started:
		t.Fatal("stop permitted a future poll")
	case <-time.After(30 * time.Millisecond):
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestTrackingStatusRetainsLatestObservedTelemetryWithoutPositionTrust(t *testing.T) {
	controller := NewTrackingController(func(context.Context, string) (TelemetryResult, error) { return TelemetryResult{}, nil }, DefaultTrackingPolicy(), nil)
	controller.mu.Lock()
	controller.status = TrackingStatus{Active: true, TargetPublicKey: trackingKey, MotionState: MotionUnknown, CurrentIntervalSeconds: 30}
	controller.generation = 1
	controller.mu.Unlock()
	voltage, latitude, longitude := 3.71, 52.52, 13.405
	observed := TelemetryResult{TargetPublicKey: trackingKey, SourcePrefix: "0123456789ab", ReceivedAt: time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC), Telemetry: Telemetry{Voltage: &voltage, Latitude: &latitude, Longitude: &longitude}}
	controller.recordSuccess(trackingKey, 1, observed)
	status := controller.Status()
	if status.LatestTelemetry == nil || status.LatestTelemetry.Telemetry.Latitude == nil || *status.LatestTelemetry.Telemetry.Latitude != latitude {
		t.Fatalf("latest telemetry missing: %#v", status.LatestTelemetry)
	}
	*status.LatestTelemetry.Telemetry.Latitude = 0
	if got := controller.Status().LatestTelemetry.Telemetry.Latitude; got == nil || *got != latitude {
		t.Fatalf("latest telemetry was not defensively copied: %#v", got)
	}
}

func TestTrackingNewControllerDoesNotResume(t *testing.T) {
	controller := NewTrackingController(func(context.Context, string) (TelemetryResult, error) {
		t.Fatal("new controller polled without start")
		return TelemetryResult{}, nil
	}, DefaultTrackingPolicy(), nil)
	if status := controller.Status(); status.Active || status.TargetPublicKey != "" || status.MotionState != MotionUnknown {
		t.Fatalf("initial status=%#v", status)
	}
}

func TestTrackingStopClearsRouteRecoveryEpisodeState(t *testing.T) {
	controller := NewTrackingControllerWithRouteRecovery(func(context.Context, string) (TelemetryResult, error) {
		return TelemetryResult{}, nil
	}, func(context.Context, string) error { return nil }, DefaultTrackingPolicy(), nil)
	key := strings.Repeat("a", 64)
	if _, err := controller.Start(key); err != nil {
		t.Fatal(err)
	}
	controller.mu.Lock()
	controller.routeFingerprint = "zero_hop:"
	controller.routeGeneration = 7
	controller.recoveryEpisodeActive = true
	controller.pendingRecoveryEvent = "path_reset_acknowledged"
	now := time.Now().UTC()
	controller.status.RouteRecoveryState = "recovery_active"
	controller.status.LastRouteRecoveryEvent = "path_reset_acknowledged"
	controller.status.RecoveryEpisodeActive = true
	controller.status.RouteGeneration = 7
	controller.status.RouteFingerprint = "zero_hop:"
	controller.status.LastPathUpdateAt = &now
	controller.status.PathUpdatePending = true
	controller.mu.Unlock()

	status := controller.Stop()
	if status.Active || status.RouteRecoveryState != "idle" || status.LastRouteRecoveryEvent != "" || status.RecoveryEpisodeActive || status.RouteGeneration != 0 || status.RouteFingerprint != "" || status.LastPathUpdateAt != nil || status.PathUpdatePending {
		t.Fatalf("Stop preserved route-recovery state: %#v", status)
	}
	controller.mu.RLock()
	defer controller.mu.RUnlock()
	if controller.routeFingerprint != "" || controller.routeGeneration != 0 || controller.recoveryEpisodeActive || controller.pendingRecoveryEvent != "" {
		t.Fatalf("Stop preserved private route-recovery state")
	}
}

func TestTrackingInfersSpeedAndMotionBands(t *testing.T) {
	tests := []struct {
		name     string
		lat2     float64
		elapsed  time.Duration
		want     MotionState
		min, max float64
	}{
		{"stationary", 52, 10 * time.Minute, MotionStationary, 0, .01},
		{"slow", 52.009, 10 * time.Minute, MotionSlow, 5, 7},
		{"fast", 52.009, time.Minute, MotionFast, 50, 70},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := activeTrackingController()
			at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
			controller.recordSuccess(trackingKey, 1, telemetryFix(52, 13, at))
			controller.recordSuccess(trackingKey, 1, telemetryFix(test.lat2, 13, at.Add(test.elapsed)))
			status := controller.Status()
			if status.MotionState != test.want || status.EstimatedSpeedKmh == nil || *status.EstimatedSpeedKmh < test.min || *status.EstimatedSpeedKmh > test.max {
				t.Fatalf("status=%#v", status)
			}
		})
	}
}

func TestTrackingHysteresisRequiresConfirmationForBandChanges(t *testing.T) {
	controller := activeTrackingController()
	at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	controller.recordSuccess(trackingKey, 1, telemetryFix(52, 13, at))
	controller.recordSuccess(trackingKey, 1, telemetryFix(52, 13, at.Add(10*time.Minute)))
	controller.recordSuccess(trackingKey, 1, telemetryFix(52.009, 13, at.Add(11*time.Minute)))
	if got := controller.Status().MotionState; got != MotionStationary {
		t.Fatalf("single fast sample changed state to %q", got)
	}
	controller.recordSuccess(trackingKey, 1, telemetryFix(52.018, 13, at.Add(12*time.Minute)))
	if got := controller.Status().MotionState; got != MotionFast {
		t.Fatalf("confirmed fast samples state=%q", got)
	}
}

func TestTrackingIgnoresMissingInvalidAndAbsurdGPS(t *testing.T) {
	controller := activeTrackingController()
	at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	controller.recordSuccess(trackingKey, 1, TelemetryResult{ReceivedAt: at})
	if status := controller.Status(); status.LastError == nil || status.EstimatedSpeedKmh != nil {
		t.Fatalf("missing GPS status=%#v", status)
	}
	badLat, lon := 91.0, 13.0
	controller.recordSuccess(trackingKey, 1, TelemetryResult{ReceivedAt: at.Add(time.Minute), Telemetry: Telemetry{Latitude: &badLat, Longitude: &lon}})
	if status := controller.Status(); status.LastError == nil {
		t.Fatalf("invalid GPS status=%#v", status)
	}
	controller.recordSuccess(trackingKey, 1, telemetryFix(52, 13, at.Add(2*time.Minute)))
	controller.recordSuccess(trackingKey, 1, telemetryFix(53, 13, at.Add(3*time.Minute)))
	if status := controller.Status(); status.EstimatedSpeedKmh != nil || status.LastError == nil {
		t.Fatalf("jump should be ignored: %#v", status)
	}
	controller.recordSuccess(trackingKey, 1, telemetryFix(52.009, 13, at.Add(2*time.Minute+5*time.Second)))
	if status := controller.Status(); status.EstimatedSpeedKmh != nil || status.LastError == nil {
		t.Fatalf("implausible speed should be ignored: %#v", status)
	}
}

func TestTrackingFailureBackoffCapsAndSuccessfulResponseResets(t *testing.T) {
	controller := activeTrackingController()
	controller.recordFailure(trackingKey, 1, TelemetryResult{}, ErrTelemetryTimeout)
	if status := controller.Status(); status.ConsecutiveFailures != 1 || status.CurrentIntervalSeconds != 30 {
		t.Fatalf("first failure=%#v", status)
	}
	for i := 0; i < 8; i++ {
		controller.recordFailure(trackingKey, 1, TelemetryResult{}, ErrTelemetryAdapterDisconnected)
	}
	if status := controller.Status(); status.ConsecutiveFailures != 9 || status.CurrentIntervalSeconds != 300 {
		t.Fatalf("capped backoff=%#v", status)
	}
	controller.recordSuccess(trackingKey, 1, telemetryFix(52, 13, time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)))
	if status := controller.Status(); status.ConsecutiveFailures != 0 || status.CurrentIntervalSeconds != 30 || status.LastError != nil {
		t.Fatalf("success did not reset=%#v", status)
	}
}

func TestTrackingZeroHopTimeoutResetsPathOnceAndRetainsBackoff(t *testing.T) {
	controller := activeTrackingController()
	var resets atomic.Int32
	controller.reset = func(context.Context, string) error {
		resets.Add(1)
		return nil
	}
	zeroHopTimeout := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path+response_sent"}}
	if recovery := controller.recoverStaleRouteTimeout(context.Background(), trackingKey, 1, zeroHopTimeout, ErrTelemetryTimeout); recovery != "path_reset_acknowledged" || resets.Load() != 1 {
		t.Fatalf("first recovery=%q resets=%d", recovery, resets.Load())
	}
	controller.recordFailure(trackingKey, 1, zeroHopTimeout, ErrTelemetryTimeout)
	status := controller.Status()
	if status.ConsecutiveFailures != 1 || status.CurrentIntervalSeconds != 30 || status.RouteRecoveryState != "recovery_active" || !status.RecoveryEpisodeActive || status.LastRouteRecoveryEvent != "path_reset_acknowledged" || len(status.RecentPolls) != 1 || status.RecentPolls[0].RouteRecovery != "path_reset_acknowledged" {
		t.Fatalf("post-reset timeout status=%#v", status)
	}
	if recovery := controller.recoverStaleRouteTimeout(context.Background(), trackingKey, 1, zeroHopTimeout, ErrTelemetryTimeout); recovery != "" || resets.Load() != 1 {
		t.Fatalf("repeated recovery=%q resets=%d", recovery, resets.Load())
	}
	controller.recordFailure(trackingKey, 1, zeroHopTimeout, ErrTelemetryTimeout)
	if status = controller.Status(); status.ConsecutiveFailures != 2 || status.CurrentIntervalSeconds != 60 || resets.Load() != 1 {
		t.Fatalf("backoff or reset loop changed: %#v resets=%d", status, resets.Load())
	}
}

func TestTrackingRouteRecoveryRecordsFloodThenExplicitPathAndPathUpdate(t *testing.T) {
	controller := activeTrackingController()
	controller.recoveryEpisodeActive = true
	controller.status.RouteRecoveryState = "recovery_active"
	flood := telemetryFix(52, 13, time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC))
	flood.RouteAttempt = RouteEvidence{Mode: RouteModeFlood, Source: "contact_out_path+response_sent"}
	controller.recordSuccess(trackingKey, 1, flood)
	if status := controller.Status(); status.RouteRecoveryState != "recovery_complete" || status.RecoveryEpisodeActive || status.RecentPolls[0].RouteAttempt.Mode != RouteModeFlood || status.RecentPolls[0].RouteRecovery != "flood_attempted" {
		t.Fatalf("flood attempt status=%#v", status)
	}
	updatedAt := time.Date(2026, 9, 13, 10, 1, 0, 0, time.UTC)
	controller.HandlePathUpdated(trackingKey, updatedAt)
	if status := controller.Status(); !status.PathUpdatePending || status.LastPathUpdateAt == nil || !status.LastPathUpdateAt.Equal(updatedAt) {
		t.Fatalf("path update status=%#v", status)
	}
	explicit := telemetryFix(52.001, 13, updatedAt.Add(time.Minute))
	explicit.RouteAttempt = RouteEvidence{Mode: RouteModeExplicitPath, Path: []string{"aa", "bb"}, PathLength: 2, Source: "contact_out_path+response_sent"}
	controller.recordSuccess(trackingKey, 1, explicit)
	status := controller.Status()
	last := status.RecentPolls[len(status.RecentPolls)-1]
	if last.RouteAttempt.Mode != RouteModeExplicitPath || !last.PathUpdateObserved || status.PathUpdatePending {
		t.Fatalf("explicit path status=%#v last=%#v", status, last)
	}
}

func TestTrackingPathResetFailureAndStoppedControllerAreSafe(t *testing.T) {
	controller := activeTrackingController()
	controller.reset = func(context.Context, string) error { return ErrPathResetFirmware }
	result := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path"}}
	if got := controller.recoverStaleRouteTimeout(context.Background(), trackingKey, 1, result, ErrTelemetryTimeout); got != "path_reset_failed" {
		t.Fatalf("recovery=%q", got)
	}
	controller.recordFailure(trackingKey, 1, result, ErrTelemetryTimeout)
	if status := controller.Status(); status.RouteRecoveryState != "recovery_active" || !status.RecoveryEpisodeActive || status.LastRouteRecoveryEvent != "path_reset_failed" || status.CurrentIntervalSeconds != 30 {
		t.Fatalf("failed reset broke tracking: %#v", status)
	}
	var resets atomic.Int32
	controller = activeTrackingController()
	controller.reset = func(context.Context, string) error { resets.Add(1); return nil }
	controller.Stop()
	if got := controller.recoverStaleRouteTimeout(context.Background(), trackingKey, 1, result, ErrTelemetryTimeout); got != "" || resets.Load() != 0 {
		t.Fatalf("stopped controller performed recovery=%q resets=%d", got, resets.Load())
	}
}

func TestTrackingRouteRecoveryEpisodesAreRepeatableAndActual(t *testing.T) {
	controller := activeTrackingController()
	var resets atomic.Int32
	controller.reset = func(context.Context, string) error { resets.Add(1); return nil }
	at := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	zero := func(at time.Time) TelemetryResult {
		result := telemetryFix(52, 13, at)
		result.RouteAttempt = RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path+response_sent"}
		return result
	}
	flood := func(at time.Time) TelemetryResult {
		result := telemetryFix(52.001, 13, at)
		result.RouteAttempt = RouteEvidence{Mode: RouteModeFlood, Source: "contact_out_path+response_sent"}
		return result
	}

	// Route generation 1 is known-good zero-hop. Its later timeout opens the
	// first episode and resets exactly once.
	controller.recordSuccess(trackingKey, 1, zero(at))
	if controller.Status().RouteGeneration != 1 {
		t.Fatalf("initial route generation=%d", controller.Status().RouteGeneration)
	}
	controller.recoverStaleRouteTimeout(context.Background(), trackingKey, 1, zero(at.Add(time.Minute)), ErrTelemetryTimeout)
	controller.recordFailure(trackingKey, 1, zero(at.Add(time.Minute)), ErrTelemetryTimeout)
	if status := controller.Status(); resets.Load() != 1 || !status.RecoveryEpisodeActive || status.RecentPolls[len(status.RecentPolls)-1].RouteRecovery != "path_reset_acknowledged" {
		t.Fatalf("first episode resets=%d status=%#v", resets.Load(), status)
	}

	// A failing flood is visible as flood_attempted but cannot reset again.
	controller.recordFailure(trackingKey, 1, flood(at.Add(2*time.Minute)), ErrTelemetryTimeout)
	last := controller.Status().RecentPolls[len(controller.Status().RecentPolls)-1]
	if last.RouteRecovery != "flood_attempted" || resets.Load() != 1 || !controller.Status().RecoveryEpisodeActive {
		t.Fatalf("flood timeout history=%#v resets=%d status=%#v", last, resets.Load(), controller.Status())
	}

	// A successful flood closes the episode. A later learned zero-hop route is
	// a new generation and can independently become stale.
	controller.recordSuccess(trackingKey, 1, flood(at.Add(3*time.Minute)))
	if status := controller.Status(); status.RecoveryEpisodeActive || status.RecentPolls[len(status.RecentPolls)-1].RouteRecovery != "flood_attempted" {
		t.Fatalf("flood recovery did not close: %#v", status)
	}
	controller.recordSuccess(trackingKey, 1, zero(at.Add(4*time.Minute)))
	controller.recoverStaleRouteTimeout(context.Background(), trackingKey, 1, zero(at.Add(5*time.Minute)), ErrTelemetryTimeout)
	if resets.Load() != 2 || !controller.Status().RecoveryEpisodeActive {
		t.Fatalf("later zero-hop did not open a new episode: resets=%d status=%#v", resets.Load(), controller.Status())
	}
}

func TestTrackingRecoveryNeverLabelsFloodWithoutAnActualFloodPoll(t *testing.T) {
	controller := activeTrackingController()
	controller.recoveryEpisodeActive = true
	controller.status.RouteRecoveryState = "recovery_active"
	controller.pendingRecoveryEvent = "path_reset_acknowledged"
	zero := telemetryFix(52, 13, time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC))
	zero.RouteAttempt = RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path+response_sent"}
	controller.recordSuccess(trackingKey, 1, zero)
	poll := controller.Status().RecentPolls[0]
	if poll.RouteRecovery == "flood_attempted" || poll.RouteRecovery != "path_reset_acknowledged" || poll.RouteAttempt.Mode != RouteModeZeroHop {
		t.Fatalf("zero-hop poll falsely attributed flood: %#v", poll)
	}
}

func TestTrackingExplicitPathCanStartLaterRecoveryEpisode(t *testing.T) {
	controller := activeTrackingController()
	var resets atomic.Int32
	controller.reset = func(context.Context, string) error { resets.Add(1); return nil }
	explicit := telemetryFix(52, 13, time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC))
	explicit.RouteAttempt = RouteEvidence{Mode: RouteModeExplicitPath, Path: []string{"aa", "bb"}, PathLength: 2, Source: "contact_out_path+response_sent"}
	controller.recordSuccess(trackingKey, 1, explicit)
	controller.recoverStaleRouteTimeout(context.Background(), trackingKey, 1, explicit, ErrTelemetryTimeout)
	if resets.Load() != 1 || !controller.Status().RecoveryEpisodeActive {
		t.Fatalf("explicit route did not start recovery: resets=%d status=%#v", resets.Load(), controller.Status())
	}
}

func activeTrackingController() *TrackingController {
	controller := NewTrackingController(func(context.Context, string) (TelemetryResult, error) { return TelemetryResult{}, nil }, DefaultTrackingPolicy(), nil)
	controller.generation = 1
	controller.status = TrackingStatus{Active: true, TargetPublicKey: trackingKey, MotionState: MotionUnknown, CurrentIntervalSeconds: 30}
	return controller
}

func telemetryFix(latitude, longitude float64, observedAt time.Time) TelemetryResult {
	return TelemetryResult{ReceivedAt: observedAt, Telemetry: Telemetry{Latitude: &latitude, Longitude: &longitude}}
}
