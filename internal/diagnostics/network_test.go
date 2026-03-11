package diagnostics

import "testing"

func TestNetworkAvailable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		probe NetworkProbe
		avail bool
		known bool
	}{
		{
			name:  "available",
			probe: NetworkProbe{Status: "available"},
			avail: true,
			known: true,
		},
		{
			name:  "unavailable",
			probe: NetworkProbe{Status: "unavailable"},
			avail: false,
			known: true,
		},
		{
			name:  "unknown",
			probe: NetworkProbe{Status: "unknown"},
			avail: false,
			known: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			avail, known := NetworkAvailable(tc.probe)
			if avail != tc.avail || known != tc.known {
				t.Fatalf("expected (%t,%t), got (%t,%t)", tc.avail, tc.known, avail, known)
			}
		})
	}
}

func TestNetworkComponentState(t *testing.T) {
	t.Parallel()

	state, msg := NetworkComponentState(NetworkProbe{
		Status:    "available",
		Interface: "wlan0",
		Address:   "192.168.50.2",
	})
	if state != "available" {
		t.Fatalf("expected available state, got %s", state)
	}
	if msg == "" {
		t.Fatal("expected non-empty message")
	}

	state, msg = NetworkComponentState(NetworkProbe{
		Status: "unavailable",
		Detail: "no dhcp lease",
	})
	if state != "unavailable" {
		t.Fatalf("expected unavailable state, got %s", state)
	}
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
}
