//go:build linux

package meshcore

import "testing"

func TestBluezConnectionCloseClosesPrivateBusExactlyOnce(t *testing.T) {
	closes := 0
	connection := &bluezConnection{
		done: make(chan struct{}),
		closeBus: func() error {
			closes++
			return nil
		},
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if closes != 1 {
		t.Fatalf("private D-Bus closes=%d, want 1", closes)
	}
	select {
	case <-connection.done:
	default:
		t.Fatal("connection close did not unblock notification readers")
	}
}
