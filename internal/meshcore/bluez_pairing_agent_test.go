package meshcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestBluezPairAgentRequestPasskeyUsesSelectedDeviceAndSixDigitPIN(t *testing.T) {
	t.Parallel()
	device := dbus.ObjectPath("/org/bluez/hci0/dev_01_23_45_67_89_AB")
	agent := &bluezPairAgent{device: device, pin: "012345"}
	passkey, err := agent.RequestPasskey(device)
	if err != nil || passkey != 12345 {
		t.Fatalf("RequestPasskey = %d, %v; want 12345, nil", passkey, err)
	}
	pincode, err := agent.RequestPinCode(device)
	if err != nil || pincode != "012345" {
		t.Fatalf("RequestPinCode = %q, %v; want preserved compatibility PIN", pincode, err)
	}
}

func TestBluezPairingDiagnosticClassifiesWithoutLeakingDBusBody(t *testing.T) {
	t.Parallel()
	err := bluezPairingDiagnostic("pair", dbus.NewError("org.bluez.Error.InProgress", []interface{}{"PIN 012345"}))
	if !errors.Is(err, ErrBLEPairingFailed) {
		t.Fatalf("diagnostic must remain a BLE pairing failure: %v", err)
	}
	diagnostic, ok := err.(*BLEPairingDiagnostic)
	if !ok || diagnostic.Operation != "pair" || diagnostic.Code != "busy" {
		t.Fatalf("diagnostic = %#v; want safe pair/busy classification", err)
	}
	if strings.Contains(err.Error(), "012345") {
		t.Fatalf("diagnostic leaked D-Bus body: %v", err)
	}
}

func TestBluezPairAgentRejectsWrongOrInvalidPasskeyRequestsWithoutLeakingPIN(t *testing.T) {
	t.Parallel()
	device := dbus.ObjectPath("/org/bluez/hci0/dev_01_23_45_67_89_AB")
	agent := &bluezPairAgent{device: device, pin: "654321"}
	if _, err := agent.RequestPasskey("/org/bluez/hci0/dev_OTHER"); err == nil || strings.Contains(err.Error(), "654321") {
		t.Fatalf("wrong-device request must be safely rejected: %v", err)
	}
	if err := agent.RequestAuthorization("/org/bluez/hci0/dev_OTHER"); err == nil || strings.Contains(err.Error(), "654321") {
		t.Fatalf("wrong-device authorization must be safely rejected: %v", err)
	}
	agent.pin = "12AB56"
	if _, err := agent.RequestPasskey(device); err == nil || strings.Contains(err.Error(), "12AB56") {
		t.Fatalf("invalid internal PIN must fail closed: %v", err)
	}
	if value, err := agent.RequestPinCode(device); err == nil || value != "" || strings.Contains(err.Error(), "12AB56") {
		t.Fatalf("legacy PIN fallback must fail closed: %q %v", value, err)
	}
}

func TestBluezPairAgentCancelReleaseAndClearErasePIN(t *testing.T) {
	t.Parallel()
	device := dbus.ObjectPath("/org/bluez/hci0/dev_01_23_45_67_89_AB")
	for _, release := range []struct {
		name string
		call func(*bluezPairAgent) *dbus.Error
	}{
		{name: "cancel", call: (*bluezPairAgent).Cancel},
		{name: "release", call: (*bluezPairAgent).Release},
		{name: "explicit_clear", call: func(agent *bluezPairAgent) *dbus.Error { agent.clear(); return nil }},
	} {
		t.Run(release.name, func(t *testing.T) {
			agent := &bluezPairAgent{device: device, pin: "123456"}
			if err := release.call(agent); err != nil {
				t.Fatal(err)
			}
			if _, err := agent.RequestPasskey(device); err == nil || strings.Contains(err.Error(), "123456") {
				t.Fatalf("cleanup did not erase PIN safely: %v", err)
			}
		})
	}
}
