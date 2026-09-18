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

func TestSessionTaggedSchedulingAnchorsToActualRequestStart(t *testing.T) {
	now := time.Date(2026, 9, 16, 10, 0, 4, 0, time.UTC)
	started := now.Add(-4 * time.Second)
	policy := DefaultTrackingPolicy()
	for _, test := range []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{name: "fast", interval: policy.FastInterval, want: 11 * time.Second},
		{name: "slow", interval: policy.SlowInterval, want: 26 * time.Second},
		{name: "stationary", interval: policy.StationaryInterval, want: 41 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nextTrackingRequestAt(now, started, test.interval, true, true); got.Sub(now) != test.want {
				t.Fatalf("next tagged Session start = %s, want %s", got.Sub(now), test.want)
			}
		})
	}
	if got := nextTrackingRequestAt(now, started, policy.FastInterval, true, false); got.Sub(now) != policy.FastInterval {
		t.Fatalf("legacy request scheduled from completion = %s, want %s", got.Sub(now), policy.FastInterval)
	}
	if got := nextTrackingRequestAt(now, started, policy.FastInterval, true, true); got.Before(now) {
		t.Fatalf("tagged schedule is in the past: %s", got)
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

func TestTrackingStatusExposesTaggedTelemetryValidationDiagnostics(t *testing.T) {
	controller := activeTrackingController()
	requestTag, responseTag := uint32(0x11223344), uint32(0x11223344)
	result := TelemetryResult{
		ReceivedAt: time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC), Tagged: true,
		Transport: "tagged_binary", Capability: "tagged_binary_supported", RequestTag: &requestTag, ResponseTag: &responseTag,
	}
	controller.recordSuccess(trackingKey, 1, result)
	status := controller.Status()
	if status.TelemetryTransport != "tagged_binary" || status.TelemetryCapability != "tagged_binary_supported" || status.LastTelemetryRequestTag == nil || *status.LastTelemetryRequestTag != requestTag || status.LastTelemetryResponseTag == nil || *status.LastTelemetryResponseTag != responseTag {
		t.Fatalf("tagged diagnostics = %#v", status)
	}
	controller.recordFailure(trackingKey, 1, result, ErrTelemetryTimeout)
	status = controller.Status()
	if status.LastTelemetryExpiredTag == nil || *status.LastTelemetryExpiredTag != requestTag || status.RecentPolls[len(status.RecentPolls)-1].RequestTag == nil || *status.RecentPolls[len(status.RecentPolls)-1].RequestTag != requestTag {
		t.Fatalf("timeout diagnostics = %#v", status)
	}

	controller.recordSuccess(trackingKey, 1, TelemetryResult{ReceivedAt: time.Now().UTC(), Transport: "legacy", Capability: "legacy_fallback", FallbackReason: "unsupported_command"})
	status = controller.Status()
	if status.TelemetryTransport != "legacy" || status.TelemetryCapability != "legacy_fallback" || status.LastTelemetryFallbackReason != "unsupported_command" {
		t.Fatalf("fallback diagnostics = %#v", status)
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

func TestManualTrackingFailureBackoffCapsAndSuccessfulResponseResets(t *testing.T) {
	controller := activeTrackingController()
	controller.recordFailure(trackingKey, 1, TelemetryResult{}, ErrTelemetryTimeout)
	if status := controller.Status(); status.ControlSource != "" || status.ConsecutiveFailures != 1 || status.CurrentIntervalSeconds != 30 || status.LastFailureScheduleSource != "manual_backoff" {
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

func TestSessionTrackingRemoteFailuresKeepMotionDiscoveryCadence(t *testing.T) {
	tests := []struct {
		name     string
		motion   MotionState
		interval int64
	}{
		{name: "unknown", motion: MotionUnknown, interval: 30},
		{name: "fast", motion: MotionFast, interval: 15},
		{name: "slow", motion: MotionSlow, interval: 30},
		{name: "stationary", motion: MotionStationary, interval: 45},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := sessionTrackingController(test.motion)
			for attempt := 1; attempt <= 8; attempt++ {
				controller.recordFailure(trackingKey, 1, TelemetryResult{}, ErrTelemetryTimeout)
				status := controller.Status()
				if status.ControlSource != "session" || status.ConsecutiveFailures != attempt || status.CurrentIntervalSeconds != test.interval || status.MotionState != test.motion || status.LastFailureScheduleSource != "motion" {
					t.Fatalf("attempt %d status=%#v", attempt, status)
				}
				last := status.RecentPolls[len(status.RecentPolls)-1]
				if last.IntervalSeconds != test.interval || last.MotionState != test.motion || last.ConsecutiveFailures != attempt || last.ScheduleSource != "motion" {
					t.Fatalf("attempt %d poll=%#v", attempt, last)
				}
			}
		})
	}
}

func TestSessionTaggedTimeoutsKeepRequestStartCadence(t *testing.T) {
	policy := DefaultTrackingPolicy()
	for _, test := range []struct {
		name     string
		motion   MotionState
		interval time.Duration
	}{
		{name: "stationary", motion: MotionStationary, interval: policy.StationaryInterval},
		{name: "slow", motion: MotionSlow, interval: policy.SlowInterval},
		{name: "fast", motion: MotionFast, interval: policy.FastInterval},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := sessionTrackingController(test.motion)
			started := time.Date(2026, 9, 16, 11, 43, 57, 0, time.UTC)
			for attempt := 1; attempt <= 4; attempt++ {
				tag := uint32(attempt)
				// Model the physical tagged expiry occurring seven seconds after
				// start. The planner must retain the prior start as its anchor.
				controller.recordFailure(trackingKey, 1, TelemetryResult{Tagged: true, RequestTag: &tag, RequestedAt: started}, ErrTelemetryTimeout, started)
				status := controller.Status()
				if status.LastFailureScheduleSource != "motion" || status.CurrentIntervalSeconds != int64(test.interval/time.Second) || status.LastTelemetryExpiredTag == nil || *status.LastTelemetryExpiredTag != tag {
					t.Fatalf("attempt %d failure scheduling=%#v", attempt, status)
				}
				timedOutAt := started.Add(7 * time.Second)
				next := nextTrackingRequestAt(timedOutAt, started, test.interval, status.ControlSource == "session", true)
				if got := next.Sub(started); got != test.interval {
					t.Fatalf("attempt %d next start spacing=%s, want %s", attempt, got, test.interval)
				}
				started = next
			}
			responseTag := uint32(99)
			latitude, longitude := 52.0, 13.0
			controller.recordSuccess(trackingKey, 1, TelemetryResult{
				Tagged:      true,
				Transport:   "tagged_binary",
				Capability:  "tagged_binary_supported",
				RequestTag:  &responseTag,
				ResponseTag: &responseTag,
				ReceivedAt:  started,
				Telemetry:   Telemetry{Latitude: &latitude, Longitude: &longitude},
			})
			status := controller.Status()
			if status.ConsecutiveFailures != 0 || status.LastError != nil || status.LastTelemetryResponseTag == nil || *status.LastTelemetryResponseTag != responseTag {
				t.Fatalf("tagged recovery did not clear failure state: %#v", status)
			}
		})
	}
}

func TestSessionTrackingRetainsLastKnownFastMotionAndResetsOnRecovery(t *testing.T) {
	controller := sessionTrackingController(MotionUnknown)
	at := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	controller.recordSuccess(trackingKey, 1, telemetryFix(52, 13, at))
	controller.recordSuccess(trackingKey, 1, telemetryFix(52.009, 13, at.Add(time.Minute)))
	if status := controller.Status(); status.MotionState != MotionFast || status.CurrentIntervalSeconds != 15 {
		t.Fatalf("fast motion was not established: %#v", status)
	}
	for attempt := 0; attempt < 5; attempt++ {
		controller.recordFailure(trackingKey, 1, TelemetryResult{}, ErrTelemetryTimeout)
	}
	if status := controller.Status(); status.MotionState != MotionFast || status.CurrentIntervalSeconds != 15 || status.ConsecutiveFailures != 5 {
		t.Fatalf("telemetry loss changed last-known motion: %#v", status)
	}
	controller.recordSuccess(trackingKey, 1, telemetryFix(52.010, 13, at.Add(2*time.Minute)))
	if status := controller.Status(); status.ConsecutiveFailures != 0 || status.CurrentIntervalSeconds != 15 || status.LastError != nil {
		t.Fatalf("recovery did not resume motion scheduling: %#v", status)
	}
}

func TestSessionTrackingRouteRecoveryResetsOnceAndContinuesFloodDiscovery(t *testing.T) {
	controller := sessionTrackingController(MotionFast)
	var resets atomic.Int32
	controller.reset = func(context.Context, string) error { resets.Add(1); return nil }
	zeroHopTimeout := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path+response_sent"}}
	for attempt := 1; attempt < staleRouteFailureThreshold; attempt++ {
		if recovery := recordRouteTimeout(controller, zeroHopTimeout); recovery != "" {
			t.Fatalf("timeout %d recovery=%q", attempt, recovery)
		}
	}
	if recovery := recordRouteTimeout(controller, zeroHopTimeout); recovery != "path_reset_acknowledged" {
		t.Fatalf("threshold recovery=%q", recovery)
	}
	floodTimeout := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeFlood, Source: "contact_out_path+response_sent"}}
	recordRouteTimeout(controller, floodTimeout)
	status := controller.Status()
	last := status.RecentPolls[len(status.RecentPolls)-1]
	if resets.Load() != 1 || status.CurrentIntervalSeconds != 15 || !status.RecoveryEpisodeActive || last.RouteRecovery != "flood_attempted" || last.IntervalSeconds != 15 {
		t.Fatalf("Session route recovery stopped discovery: resets=%d status=%#v last=%#v", resets.Load(), status, last)
	}
}

func TestSessionTrackingAdapterDisconnectRetainsBoundedBackoff(t *testing.T) {
	controller := sessionTrackingController(MotionFast)
	for attempt, wantInterval := range []int64{30, 60, 120} {
		controller.recordFailure(trackingKey, 1, TelemetryResult{}, ErrTelemetryAdapterDisconnected)
		if status := controller.Status(); status.CurrentIntervalSeconds != wantInterval || status.ConsecutiveFailures != attempt+1 || status.LastFailureScheduleSource != "adapter_recovery" {
			t.Fatalf("adapter failure %d status=%#v", attempt+1, status)
		}
	}
}

func TestTrackingStaleRouteTimeoutThresholdResetsPathOnceAndRetainsBackoff(t *testing.T) {
	controller := activeTrackingController()
	var resets atomic.Int32
	controller.reset = func(context.Context, string) error {
		resets.Add(1)
		return nil
	}
	zeroHopTimeout := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path+response_sent"}}
	if recovery := recordRouteTimeout(controller, zeroHopTimeout); recovery != "" || resets.Load() != 0 {
		t.Fatalf("single timeout recovery=%q resets=%d", recovery, resets.Load())
	}
	if status := controller.Status(); status.StaleRouteFailures != 1 || status.RecoveryEpisodeActive || status.RecentPolls[0].StaleRouteFailures != 1 {
		t.Fatalf("single timeout did not retain route: %#v", status)
	}
	if recovery := recordRouteTimeout(controller, zeroHopTimeout); recovery != "" || resets.Load() != 0 {
		t.Fatalf("second timeout recovery=%q resets=%d", recovery, resets.Load())
	}
	if recovery := recordRouteTimeout(controller, zeroHopTimeout); recovery != "path_reset_acknowledged" || resets.Load() != 1 {
		t.Fatalf("threshold recovery=%q resets=%d", recovery, resets.Load())
	}
	status := controller.Status()
	if status.ConsecutiveFailures != 3 || status.CurrentIntervalSeconds != 120 || status.RouteRecoveryState != "recovery_active" || !status.RecoveryEpisodeActive || status.LastRouteRecoveryEvent != "path_reset_acknowledged" || len(status.RecentPolls) != 3 || status.RecentPolls[2].RouteRecovery != "path_reset_acknowledged" || status.RecentPolls[2].StaleRouteFailures != staleRouteFailureThreshold {
		t.Fatalf("post-reset timeout status=%#v", status)
	}
	if recovery := recordRouteTimeout(controller, zeroHopTimeout); recovery != "" || resets.Load() != 1 {
		t.Fatalf("repeated recovery=%q resets=%d", recovery, resets.Load())
	}
	if status = controller.Status(); status.ConsecutiveFailures != 4 || status.CurrentIntervalSeconds != 240 || resets.Load() != 1 {
		t.Fatalf("backoff or reset loop changed: %#v resets=%d", status, resets.Load())
	}
}

func TestTrackingRouteRecoveryRecordsFloodThenExplicitPathAndPathUpdate(t *testing.T) {
	controller := activeTrackingController()
	controller.recoveryEpisodeActive = true
	controller.status.RouteRecoveryState = "recovery_active"
	flood := telemetryFix(52, 13, time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC))
	flood.RouteAttempt = RouteEvidence{Mode: RouteModeFlood, Source: "contact_out_path+response_sent"}
	if staged := controller.withSuccessfulRouteEvidence(trackingKey, 1, flood); staged.RouteRecovery != "flood_attempted" || staged.PathUpdateObserved {
		t.Fatalf("flood staging evidence=%#v", staged)
	}
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
	if staged := controller.withSuccessfulRouteEvidence(trackingKey, 1, explicit); !staged.PathUpdateObserved {
		t.Fatalf("explicit-path staging evidence=%#v", staged)
	}
	controller.recordSuccess(trackingKey, 1, explicit)
	status := controller.Status()
	last := status.RecentPolls[len(status.RecentPolls)-1]
	if last.RouteAttempt.Mode != RouteModeExplicitPath || !last.PathUpdateObserved || status.PathUpdatePending {
		t.Fatalf("explicit path status=%#v last=%#v", status, last)
	}
}

func TestTrackingPathResetFailureAndStoppedControllerAreSafe(t *testing.T) {
	controller := activeTrackingController()
	var failedResets atomic.Int32
	controller.reset = func(context.Context, string) error { failedResets.Add(1); return ErrPathResetFirmware }
	result := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path+response_sent"}}
	for attempt := 1; attempt < staleRouteFailureThreshold; attempt++ {
		if got := recordRouteTimeout(controller, result); got != "" {
			t.Fatalf("timeout %d recovery=%q", attempt, got)
		}
	}
	if got := recordRouteTimeout(controller, result); got != "path_reset_failed" {
		t.Fatalf("recovery=%q", got)
	}
	if status := controller.Status(); status.RouteRecoveryState != "recovery_active" || !status.RecoveryEpisodeActive || status.LastRouteRecoveryEvent != "path_reset_failed" || status.CurrentIntervalSeconds != 120 || status.PathResetAttempts != 1 || failedResets.Load() != 1 {
		t.Fatalf("failed reset broke tracking: %#v", status)
	}
	for attempt := 1; attempt <= staleRouteFailureThreshold; attempt++ {
		recordRouteTimeout(controller, result)
	}
	if status := controller.Status(); failedResets.Load() != 2 || status.PathResetAttempts != 2 || status.LastRouteRecoveryEvent != "path_reset_failed" {
		t.Fatalf("failed reset was not boundedly retried: %#v resets=%d", status, failedResets.Load())
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
	for attempt := 1; attempt < staleRouteFailureThreshold; attempt++ {
		recordRouteTimeout(controller, zero(at.Add(time.Duration(attempt)*time.Minute)))
	}
	recordRouteTimeout(controller, zero(at.Add(time.Duration(staleRouteFailureThreshold)*time.Minute)))
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
	for attempt := 1; attempt <= staleRouteFailureThreshold; attempt++ {
		recordRouteTimeout(controller, zero(at.Add(time.Duration(4+attempt)*time.Minute)))
	}
	if resets.Load() != 2 || !controller.Status().RecoveryEpisodeActive {
		t.Fatalf("later zero-hop did not open a new episode: resets=%d status=%#v", resets.Load(), controller.Status())
	}
}

func TestTrackingPathUpdateRearmsAcknowledgedRecoveryBeforeTelemetrySuccess(t *testing.T) {
	controller := activeTrackingController()
	var resets atomic.Int32
	controller.reset = func(context.Context, string) error { resets.Add(1); return nil }
	at := time.Date(2026, 9, 18, 6, 48, 36, 0, time.UTC)
	explicit := func(observedAt time.Time) TelemetryResult {
		result := telemetryFix(52, 13, observedAt)
		// Deliberately retain the same compact hash for both generations: a
		// PATH_UPDATED notice, not hash identity, proves the new generation.
		result.RouteAttempt = RouteEvidence{Mode: RouteModeExplicitPath, Path: []string{"18"}, PathLength: 1, Source: "contact_out_path+response_sent"}
		return result
	}
	flood := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeFlood, Source: "response_sent"}}

	// A successful explicit path A becomes stale and is reset only after the
	// normal three-timeout threshold.
	controller.recordSuccess(trackingKey, 1, explicit(at))
	for attempt := 1; attempt <= staleRouteFailureThreshold; attempt++ {
		recordRouteTimeout(controller, explicit(at.Add(time.Duration(attempt)*30*time.Second)))
	}
	if status := controller.Status(); resets.Load() != 1 || !status.RecoveryEpisodeActive || status.LastRouteRecoveryEvent != "path_reset_acknowledged" {
		t.Fatalf("route A reset status=%#v resets=%d", status, resets.Load())
	}

	// Remaining in flood after the acknowledged reset never creates another
	// reset, even across many failed requests.
	for attempt := 1; attempt <= 5; attempt++ {
		recordRouteTimeout(controller, flood)
	}
	if status := controller.Status(); resets.Load() != 1 || !status.RecoveryEpisodeActive || !controller.floodObservedAfterReset {
		t.Fatalf("flood failures created a reset or lost transition state: %#v resets=%d", status, resets.Load())
	}

	// Companion can publish PATH_UPDATED and learn explicit path B before any
	// telemetry response is accepted. The first B timeout rearms a new episode,
	// and B becomes eligible for its own reset at the same threshold.
	controller.HandlePathUpdated(trackingKey, at.Add(6*time.Minute))
	for attempt := 1; attempt < staleRouteFailureThreshold; attempt++ {
		if got := recordRouteTimeout(controller, explicit(at.Add(time.Duration(6+attempt)*time.Minute))); got != "" {
			t.Fatalf("route B timeout %d recovery=%q", attempt, got)
		}
	}
	status := controller.Status()
	firstB := status.RecentPolls[len(status.RecentPolls)-2]
	if firstB.RouteRecovery != "new_route_observed" || !firstB.PathUpdateObserved || firstB.StaleRouteFailures != 1 || status.RecoveryEpisodeActive || status.StaleRouteFailures != staleRouteFailureThreshold-1 || resets.Load() != 1 {
		t.Fatalf("route B was not rearmed: %#v firstB=%#v resets=%d", status, firstB, resets.Load())
	}
	if got := recordRouteTimeout(controller, explicit(at.Add(9*time.Minute))); got != "path_reset_acknowledged" {
		t.Fatalf("route B threshold recovery=%q", got)
	}
	if status := controller.Status(); resets.Load() != 2 || !status.RecoveryEpisodeActive || status.LastRouteRecoveryEvent != "path_reset_acknowledged" || status.PathResetAttempts != 1 {
		t.Fatalf("route B did not receive a bounded second reset: %#v resets=%d", status, resets.Load())
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
	for attempt := 1; attempt <= staleRouteFailureThreshold; attempt++ {
		recordRouteTimeout(controller, explicit)
	}
	if resets.Load() != 1 || !controller.Status().RecoveryEpisodeActive {
		t.Fatalf("explicit route did not start recovery: resets=%d status=%#v", resets.Load(), controller.Status())
	}
}

func TestTrackingExplicitPathFailureStreakResetsAfterSuccess(t *testing.T) {
	controller := activeTrackingController()
	var resets atomic.Int32
	controller.reset = func(context.Context, string) error { resets.Add(1); return nil }
	explicit := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeExplicitPath, Path: []string{"18"}, PathLength: 1, Source: "contact_out_path+response_sent"}}
	for attempt := 1; attempt < staleRouteFailureThreshold; attempt++ {
		if got := recordRouteTimeout(controller, explicit); got != "" {
			t.Fatalf("timeout %d recovery=%q", attempt, got)
		}
	}
	if status := controller.Status(); status.StaleRouteFailures != staleRouteFailureThreshold-1 || resets.Load() != 0 {
		t.Fatalf("pre-success streak=%#v resets=%d", status, resets.Load())
	}
	explicit.ReceivedAt = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	controller.recordSuccess(trackingKey, 1, explicit)
	if status := controller.Status(); status.StaleRouteFailures != 0 || status.ConsecutiveFailures != 0 || status.RecoveryEpisodeActive {
		t.Fatalf("success did not reset stale-route state: %#v", status)
	}
	if got := recordRouteTimeout(controller, explicit); got != "" {
		t.Fatalf("first timeout after success recovery=%q", got)
	}
	if status := controller.Status(); status.StaleRouteFailures != 1 || resets.Load() != 0 {
		t.Fatalf("post-success timeout did not start a new streak: %#v resets=%d", status, resets.Load())
	}
}

func TestTrackingContactSnapshotWithoutSendAcknowledgementCannotResetPath(t *testing.T) {
	controller := activeTrackingController()
	var resets atomic.Int32
	controller.reset = func(context.Context, string) error { resets.Add(1); return nil }
	snapshotOnly := TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeExplicitPath, Path: []string{"18"}, PathLength: 1, Source: "contact_out_path"}}
	for attempt := 0; attempt < staleRouteFailureThreshold+1; attempt++ {
		if got := recordRouteTimeout(controller, snapshotOnly); got != "" {
			t.Fatalf("snapshot-only timeout %d recovery=%q", attempt+1, got)
		}
	}
	if status := controller.Status(); resets.Load() != 0 || status.StaleRouteFailures != 0 || status.RecoveryEpisodeActive {
		t.Fatalf("snapshot-only route changed Companion state: %#v resets=%d", status, resets.Load())
	}
}

func recordRouteTimeout(controller *TrackingController, result TelemetryResult) string {
	recovery := controller.recoverStaleRouteTimeout(context.Background(), trackingKey, 1, result, ErrTelemetryTimeout)
	controller.recordFailure(trackingKey, 1, result, ErrTelemetryTimeout)
	return recovery
}

func activeTrackingController() *TrackingController {
	controller := NewTrackingController(func(context.Context, string) (TelemetryResult, error) { return TelemetryResult{}, nil }, DefaultTrackingPolicy(), nil)
	controller.generation = 1
	controller.status = TrackingStatus{Active: true, TargetPublicKey: trackingKey, MotionState: MotionUnknown, CurrentIntervalSeconds: 30}
	return controller
}

func sessionTrackingController(motion MotionState) *TrackingController {
	controller := activeTrackingController()
	controller.SetSessionManaged(true)
	controller.mu.Lock()
	controller.status.MotionState = motion
	controller.status.CurrentIntervalSeconds = int64(controller.intervalForLocked() / time.Second)
	controller.mu.Unlock()
	return controller
}

func telemetryFix(latitude, longitude float64, observedAt time.Time) TelemetryResult {
	return TelemetryResult{ReceivedAt: observedAt, Telemetry: Telemetry{Latitude: &latitude, Longitude: &longitude}}
}
