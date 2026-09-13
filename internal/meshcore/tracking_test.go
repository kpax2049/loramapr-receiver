package meshcore

import (
	"context"
	"errors"
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

func TestTrackingNewControllerDoesNotResume(t *testing.T) {
	controller := NewTrackingController(func(context.Context, string) (TelemetryResult, error) {
		t.Fatal("new controller polled without start")
		return TelemetryResult{}, nil
	}, DefaultTrackingPolicy(), nil)
	if status := controller.Status(); status.Active || status.TargetPublicKey != "" || status.MotionState != MotionUnknown {
		t.Fatalf("initial status=%#v", status)
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
	controller.recordFailure(trackingKey, 1, ErrTelemetryTimeout)
	if status := controller.Status(); status.ConsecutiveFailures != 1 || status.CurrentIntervalSeconds != 30 {
		t.Fatalf("first failure=%#v", status)
	}
	for i := 0; i < 8; i++ {
		controller.recordFailure(trackingKey, 1, ErrTelemetryAdapterDisconnected)
	}
	if status := controller.Status(); status.ConsecutiveFailures != 9 || status.CurrentIntervalSeconds != 300 {
		t.Fatalf("capped backoff=%#v", status)
	}
	controller.recordSuccess(trackingKey, 1, telemetryFix(52, 13, time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)))
	if status := controller.Status(); status.ConsecutiveFailures != 0 || status.CurrentIntervalSeconds != 30 || status.LastError != nil {
		t.Fatalf("success did not reset=%#v", status)
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
