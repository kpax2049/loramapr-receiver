package meshcore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	MaxPayloadSize = 176
	outboundMarker = byte('<')
	inboundMarker  = byte('>')
)

var (
	ErrUnexpectedFrameMarker = errors.New("meshcore serial frame has unexpected direction marker")
	ErrInvalidFrameLength    = errors.New("meshcore serial frame has invalid length")
	ErrOversizedFrame        = errors.New("meshcore serial frame exceeds maximum payload")
	ErrTruncatedFrame        = errors.New("meshcore serial frame is truncated")
)

// WriteFrame writes one host-to-Companion physical serial frame. The framing is
// pinned to MeshCore Companion v1.17.1: '<', uint16 little-endian length, then
// the command payload.
func WriteFrame(writer io.Writer, payload []byte) error {
	if len(payload) == 0 {
		return ErrInvalidFrameLength
	}
	if len(payload) > MaxPayloadSize {
		return fmt.Errorf("%w: got %d bytes, maximum is %d", ErrOversizedFrame, len(payload), MaxPayloadSize)
	}

	frame := make([]byte, 3+len(payload))
	frame[0] = outboundMarker
	binary.LittleEndian.PutUint16(frame[1:3], uint16(len(payload)))
	copy(frame[3:], payload)
	if err := writeAll(writer, frame); err != nil {
		return fmt.Errorf("write meshcore serial frame: %w", err)
	}
	return nil
}

// ReadFrame reads exactly one Companion-to-host physical serial frame. A
// malformed marker, invalid/oversized length, or truncated payload is terminal
// for the stream; callers must close the stream and reconnect rather than try
// to resynchronize within ambiguous bytes.
func ReadFrame(reader io.Reader) ([]byte, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrTruncatedFrame, err)
	}
	if header[0] != inboundMarker {
		return nil, fmt.Errorf("%w: got 0x%02x, want 0x%02x", ErrUnexpectedFrameMarker, header[0], inboundMarker)
	}

	length := int(binary.LittleEndian.Uint16(header[1:3]))
	if length == 0 {
		return nil, ErrInvalidFrameLength
	}
	if length > MaxPayloadSize {
		return nil, fmt.Errorf("%w: got %d bytes, maximum is %d", ErrOversizedFrame, length, MaxPayloadSize)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrTruncatedFrame, err)
	}
	return payload, nil
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}
