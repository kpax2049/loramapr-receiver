package meshcore

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestWriteFrameUsesHostDirectionAndLittleEndianLength(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	payload := []byte{CommandDeviceQuery, ProtocolVersion, 0xAA}
	if err := WriteFrame(&output, payload); err != nil {
		t.Fatalf("WriteFrame returned error: %v", err)
	}
	want := []byte{'<', 0x03, 0x00, CommandDeviceQuery, ProtocolVersion, 0xAA}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("unexpected frame: got %x want %x", output.Bytes(), want)
	}
}

func TestReadFrameAcceptsOnlyCompanionDirection(t *testing.T) {
	t.Parallel()

	payload, err := ReadFrame(bytes.NewReader([]byte{'>', 0x03, 0x00, 0x84, 0x01, 0x02}))
	if err != nil {
		t.Fatalf("ReadFrame returned error: %v", err)
	}
	if !bytes.Equal(payload, []byte{0x84, 0x01, 0x02}) {
		t.Fatalf("unexpected payload: %x", payload)
	}

	_, err = ReadFrame(bytes.NewReader([]byte{'<', 0x01, 0x00, 0x84}))
	if !errors.Is(err, ErrUnexpectedFrameMarker) {
		t.Fatalf("expected direction marker error, got %v", err)
	}
}

func TestReadFrameHandlesPartialReadsAndBufferedFrames(t *testing.T) {
	t.Parallel()

	stream := &oneByteReader{reader: bytes.NewReader([]byte{
		'>', 0x02, 0x00, ResponseDeviceInfo, ProtocolVersion,
		'>', 0x01, 0x00, PushNewAdvert,
	})}
	first, err := ReadFrame(stream)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReadFrame(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, []byte{ResponseDeviceInfo, ProtocolVersion}) || !bytes.Equal(second, []byte{PushNewAdvert}) {
		t.Fatalf("unexpected buffered frames: %x / %x", first, second)
	}
}

func TestReadFrameRejectsInvalidOversizedAndTruncatedFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		frame []byte
		want  error
	}{
		{name: "truncated header", frame: []byte{'>'}, want: ErrTruncatedFrame},
		{name: "zero length", frame: []byte{'>', 0x00, 0x00}, want: ErrInvalidFrameLength},
		{name: "oversized", frame: []byte{'>', 0xB1, 0x00}, want: ErrOversizedFrame},
		{name: "truncated payload", frame: []byte{'>', 0x03, 0x00, 0x84}, want: ErrTruncatedFrame},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadFrame(bytes.NewReader(test.frame))
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestWriteFrameRejectsInvalidAndOversizedPayloads(t *testing.T) {
	t.Parallel()

	if err := WriteFrame(io.Discard, nil); !errors.Is(err, ErrInvalidFrameLength) {
		t.Fatalf("expected invalid length error, got %v", err)
	}
	if err := WriteFrame(io.Discard, make([]byte, MaxPayloadSize+1)); !errors.Is(err, ErrOversizedFrame) {
		t.Fatalf("expected oversized frame error, got %v", err)
	}
}

type oneByteReader struct{ reader io.Reader }

func (r *oneByteReader) Read(value []byte) (int, error) {
	if len(value) > 1 {
		value = value[:1]
	}
	return r.reader.Read(value)
}
