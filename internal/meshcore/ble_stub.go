//go:build !linux

package meshcore

import "context"

type unsupportedBLEBackend struct{}

func newSystemBLEBackend() BLEBackend { return unsupportedBLEBackend{} }

func (unsupportedBLEBackend) Discover(ctx context.Context, adapter string) ([]BLEDevice, error) {
	return nil, ErrBLEUnsupported
}
func (unsupportedBLEBackend) Connect(ctx context.Context, cfg BLEConfig) (BLEConnection, error) {
	return nil, ErrBLEUnsupported
}
func (unsupportedBLEBackend) Disconnect(ctx context.Context, cfg BLEConfig) error {
	return ErrBLEUnsupported
}
func (unsupportedBLEBackend) Pair(ctx context.Context, cfg BLEConfig, pin string) error {
	return ErrBLEUnsupported
}
func (unsupportedBLEBackend) Forget(ctx context.Context, cfg BLEConfig) error {
	return ErrBLEUnsupported
}
