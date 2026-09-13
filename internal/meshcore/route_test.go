package meshcore

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRouteEvidenceFromContactOutPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		encoding byte
		path     []byte
		mode     RouteMode
		wantPath []string
	}{
		{name: "zero hop", encoding: 0, mode: RouteModeZeroHop},
		{name: "explicit two one-byte hashes", encoding: 2, path: []byte{0xaa, 0xbb}, mode: RouteModeExplicitPath, wantPath: []string{"aa", "bb"}},
		{name: "flood unknown sentinel", encoding: contactOutPathUnknown, mode: RouteModeFlood},
		{name: "invalid reserved hash size", encoding: 0xc1, path: []byte{1, 2, 3, 4}, mode: RouteModeUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := routeEvidenceFromContactOutPath(test.encoding, test.path)
			if got.Mode != test.mode || len(got.Path) != len(test.wantPath) {
				t.Fatalf("route evidence = %#v", got)
			}
			for i := range test.wantPath {
				if got.Path[i] != test.wantPath[i] {
					t.Fatalf("path = %#v", got.Path)
				}
			}
		})
	}
}

func TestTelemetryTimeoutPreservesAttemptedRouteWithoutResponseRoute(t *testing.T) {
	key := trackingKey
	link := &telemetryTestLink{writes: make(chan []byte, 2)}
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{Adapter: "hci0", PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.setLink(link)
	adapter.setStatus(func(status *AdapterStatus) { status.State, status.Session.State = StateConnected, SessionReady })
	completed := make(chan telemetryCompletion, 1)
	go func() {
		result, err := adapter.RequestTelemetry(context.Background(), key)
		completed <- telemetryCompletion{result: result, err: err}
	}()
	<-link.writes // CMD_GET_CONTACT_BY_KEY
	adapter.handleTelemetryContactResponse(telemetryContactFrame(t, key, 2, []byte{0xaa, 0xbb}))
	<-link.writes // CMD_SEND_TELEMETRY_REQ
	adapter.finishCurrentTelemetry(TelemetryResult{}, ErrTelemetryTimeout)
	select {
	case completion := <-completed:
		if !errors.Is(completion.err, ErrTelemetryTimeout) || completion.result.RouteAttempt.Mode != RouteModeExplicitPath || completion.result.RouteAttempt.PathLength != 2 || !completion.result.ResponseRouteUnknown {
			t.Fatalf("completion = %#v, err = %v", completion.result, completion.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed-out request did not complete")
	}
}

func TestTrackingHistoryIsBoundedAndDoesNotAttributeResponseRoute(t *testing.T) {
	controller := activeTrackingController()
	for i := 0; i < recentPollLimit+1; i++ {
		controller.recordFailure(trackingKey, 1, TelemetryResult{RouteAttempt: RouteEvidence{Mode: RouteModeFlood, Source: "response_sent"}}, ErrTelemetryTimeout)
	}
	status := controller.Status()
	if len(status.RecentPolls) != recentPollLimit || status.RecentPolls[0].ConsecutiveFailures != 2 || status.RecentPolls[recentPollLimit-1].ConsecutiveFailures != recentPollLimit+1 {
		t.Fatalf("bounded history = %#v", status.RecentPolls)
	}
	last := status.RecentPolls[recentPollLimit-1]
	if last.Outcome != "timeout" || last.RouteAttempt.Mode != RouteModeFlood || last.ResponseRoute != nil || !last.ResponseRouteUnknown {
		t.Fatalf("timeout history attributed a response route: %#v", last)
	}
	result := telemetryFix(52, 13, time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC))
	result.RouteAttempt = RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path+response_sent"}
	controller.recordSuccess(trackingKey, 1, result)
	last = controller.Status().RecentPolls[recentPollLimit-1]
	if last.Outcome != "success" || last.RouteAttempt.Mode != RouteModeZeroHop || last.ResponseRoute != nil || !last.ResponseRouteUnknown {
		t.Fatalf("success history attributed a response route: %#v", last)
	}
}
