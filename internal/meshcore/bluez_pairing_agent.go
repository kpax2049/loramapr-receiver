package meshcore

import (
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
)

// bluezPairAgent is exported only for the lifetime of one Device1.Pair call.
// Its PIN is deliberately ephemeral and is cleared on every terminal Agent1
// path as well as by bluezBackend.Pair's deferred cleanup.
type bluezPairAgent struct {
	mu     sync.Mutex
	pin    string
	device dbus.ObjectPath
}

// RequestPasskey is the BlueZ Agent1 LE Secure Connections passkey method.
// BlueZ treats the uint32 as a six-digit zero-padded value, so "012345"
// correctly becomes numeric 12345 without losing pairing semantics.
func (a *bluezPairAgent) RequestPasskey(device dbus.ObjectPath) (uint32, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pin, ok := a.validPINLocked(device)
	if !ok {
		return 0, rejectedAgentError()
	}
	var value uint32
	for _, digit := range pin {
		value = value*10 + uint32(digit-'0')
	}
	return value, nil
}

// RequestPinCode remains a strict compatibility fallback for older BlueZ
// authentication flows. It never returns a cleared or malformed PIN.
func (a *bluezPairAgent) RequestPinCode(device dbus.ObjectPath) (string, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pin, ok := a.validPINLocked(device)
	if !ok {
		return "", rejectedAgentError()
	}
	return pin, nil
}

func (a *bluezPairAgent) DisplayPinCode(device dbus.ObjectPath, _ string) *dbus.Error {
	if !a.isSelectedDevice(device) {
		return rejectedAgentError()
	}
	return nil
}

func (a *bluezPairAgent) DisplayPasskey(device dbus.ObjectPath, _ uint32, _ uint16) *dbus.Error {
	if !a.isSelectedDevice(device) {
		return rejectedAgentError()
	}
	return nil
}
func (*bluezPairAgent) RequestConfirmation(dbus.ObjectPath, uint32) *dbus.Error {
	return dbus.NewError("org.bluez.Error.Rejected", []interface{}{"confirmation unsupported"})
}
func (a *bluezPairAgent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error {
	if !a.isSelectedDevice(device) {
		return rejectedAgentError()
	}
	return nil
}
func (a *bluezPairAgent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error {
	if a.isSelectedDevice(device) && strings.EqualFold(uuid, NUSServiceUUID) {
		return nil
	}
	return dbus.NewError("org.bluez.Error.Rejected", []interface{}{"service not authorized"})
}

func (a *bluezPairAgent) isSelectedDevice(device dbus.ObjectPath) bool {
	return device == a.device
}
func (a *bluezPairAgent) Cancel() *dbus.Error  { a.clear(); return nil }
func (a *bluezPairAgent) Release() *dbus.Error { a.clear(); return nil }
func (a *bluezPairAgent) clear()               { a.mu.Lock(); a.pin = ""; a.mu.Unlock() }

func (a *bluezPairAgent) validPINLocked(device dbus.ObjectPath) (string, bool) {
	if device != a.device || len(a.pin) != 6 {
		return "", false
	}
	for _, digit := range a.pin {
		if digit < '0' || digit > '9' {
			return "", false
		}
	}
	return a.pin, true
}

func rejectedAgentError() *dbus.Error {
	return dbus.NewError("org.bluez.Error.Rejected", []interface{}{"pairing rejected"})
}

// bluezPairingDiagnostic keeps only a safe failure classification from a
// BlueZ operation. D-Bus error bodies are deliberately discarded because they
// may include peer- or agent-supplied material.
func bluezPairingDiagnostic(operation string, err error) error {
	code := "failed"
	if dbusErr, ok := err.(*dbus.Error); ok {
		name := strings.ToLower(dbusErr.Name)
		switch {
		case strings.Contains(name, "unknownmethod"), strings.Contains(name, "invalidarguments"):
			code = "agent_method_or_capability"
		case strings.Contains(name, "authentication"), strings.Contains(name, "rejected"):
			code = "authentication_rejected"
		case strings.Contains(name, "inprogress"), strings.Contains(name, "alreadyexists"), strings.Contains(name, "busy"):
			code = "busy"
		case strings.Contains(name, "notconnected"), strings.Contains(name, "notavailable"), strings.Contains(name, "noreply"), strings.Contains(name, "timedout"):
			code = "connection_failed"
		}
	}
	return newBLEPairingDiagnostic(operation, code)
}
