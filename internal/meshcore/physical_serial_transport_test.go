package meshcore

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestPhysicalSerialTransportExchangesOnlyCompleteCompanionPayloads(t *testing.T) {
	t.Parallel()

	stream := &memoryReadWriteCloser{reader: bytes.NewReader([]byte{
		'>', 0x03, 0x00, PushRawData, 0x01, 0x02,
	})}
	transport := NewPhysicalSerialTransport("/dev/ttyACM0", func(device string) (io.ReadWriteCloser, error) {
		if device != "/dev/ttyACM0" {
			t.Fatalf("opened %q", device)
		}
		return stream, nil
	})
	link, err := transport.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()

	payload, err := link.ReadFrame(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{PushRawData, 0x01, 0x02}; !bytes.Equal(payload, want) {
		t.Fatalf("payload=%x, want %x", payload, want)
	}
	if err := link.WriteFrame(context.Background(), []byte{CommandDeviceQuery, ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	if want := []byte{'<', 0x02, 0x00, CommandDeviceQuery, ProtocolVersion}; !bytes.Equal(stream.writes.Bytes(), want) {
		t.Fatalf("wire bytes=%x, want %x", stream.writes.Bytes(), want)
	}
	if metadata := link.Metadata(); metadata.Kind != "physical_serial" {
		t.Fatalf("metadata=%#v", metadata)
	}
}

type memoryReadWriteCloser struct {
	reader *bytes.Reader
	writes bytes.Buffer
}

func (s *memoryReadWriteCloser) Read(value []byte) (int, error)  { return s.reader.Read(value) }
func (s *memoryReadWriteCloser) Write(value []byte) (int, error) { return s.writes.Write(value) }
func (*memoryReadWriteCloser) Close() error                      { return nil }
