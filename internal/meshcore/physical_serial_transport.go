package meshcore

import (
	"context"
	"io"
)

// PhysicalSerialTransport is the v1.17.1 Companion physical-serial carrier.
// It owns stream opening and the '<'/'>' + uint16-little-endian framing; users
// of CompanionLink receive only complete Companion payload frames.
type PhysicalSerialTransport struct {
	device string
	open   func(string) (io.ReadWriteCloser, error)
}

func NewPhysicalSerialTransport(device string, open func(string) (io.ReadWriteCloser, error)) *PhysicalSerialTransport {
	return &PhysicalSerialTransport{device: device, open: open}
}

func (t *PhysicalSerialTransport) Open(_ context.Context) (CompanionLink, error) {
	stream, err := t.open(t.device)
	if err != nil {
		return nil, err
	}
	return &physicalSerialLink{stream: stream}, nil
}

type physicalSerialLink struct {
	stream io.ReadWriteCloser
}

func (l *physicalSerialLink) ReadFrame(_ context.Context) ([]byte, error) {
	return ReadFrame(l.stream)
}

func (l *physicalSerialLink) WriteFrame(_ context.Context, payload []byte) error {
	return WriteFrame(l.stream, payload)
}

func (l *physicalSerialLink) Metadata() TransportMetadata {
	return TransportMetadata{Kind: "physical_serial"}
}

func (l *physicalSerialLink) Close() error {
	return l.stream.Close()
}
