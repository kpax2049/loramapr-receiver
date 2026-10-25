//go:build linux

package meshcore

import (
	"errors"
	"testing"
)

func TestBluezOperationInProgress(t *testing.T) {
	if !bluezOperationInProgress(errors.New("org.bluez.Error.InProgress: operation already in progress")) {
		t.Fatal("expected BlueZ InProgress error to be retried through readiness polling")
	}
	if bluezOperationInProgress(errors.New("org.bluez.Error.Failed: refused")) {
		t.Fatal("unexpected retry classification for a non-InProgress error")
	}
}

func TestBluezRetryableNUSNotifyError(t *testing.T) {
	if !bluezRetryableNUSNotifyError(errors.New("Operation failed with ATT error: 0x0e")) {
		t.Fatal("expected transient ATT unlikely error to be retried")
	}
	if bluezRetryableNUSNotifyError(errors.New("Operation failed with ATT error: 0x0f")) {
		t.Fatal("unexpected retry classification for an authentication error")
	}
}

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
