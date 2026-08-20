package protocoladapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type testAdapter struct {
	name       string
	start      func(context.Context, AdapterSink) error
	closeCalls atomic.Int32
	snapshot   AdapterSnapshot
}

func (a *testAdapter) Name() string { return a.name }
func (a *testAdapter) Start(ctx context.Context, sink AdapterSink) error {
	return a.start(ctx, sink)
}
func (a *testAdapter) Snapshot() AdapterSnapshot { return a.snapshot }
func (a *testAdapter) Close() error {
	a.closeCalls.Add(1)
	return nil
}

func TestManagerRunsAdaptersConcurrentlyAndIsolatesFailure(t *testing.T) {
	good := &testAdapter{name: "good", start: func(ctx context.Context, sink AdapterSink) error {
		if err := sink.Publish(Event{Value: "one"}); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}}
	bad := &testAdapter{name: "bad", start: func(context.Context, AdapterSink) error {
		return errors.New("adapter failed")
	}}
	manager, err := NewManager(bad, good)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	events, err := manager.Start(context.Background())
	if err != nil {
		t.Fatalf("start manager: %v", err)
	}
	select {
	case event := <-events:
		if event.Adapter != "good" || event.Value != "one" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for healthy adapter event")
	}

	deadline := time.Now().Add(time.Second)
	for {
		snapshots := manager.Snapshots()
		if len(snapshots) == 2 && snapshots[0].Name == "bad" && snapshots[0].State == "degraded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failure was not isolated in snapshots: %#v", snapshots)
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
	if good.closeCalls.Load() != 1 || bad.closeCalls.Load() != 1 {
		t.Fatalf("expected each adapter closed once, got good=%d bad=%d", good.closeCalls.Load(), bad.closeCalls.Load())
	}
}

func TestManagerRejectsDuplicateNames(t *testing.T) {
	a := &testAdapter{name: "same", start: func(context.Context, AdapterSink) error { return nil }}
	b := &testAdapter{name: "same", start: func(context.Context, AdapterSink) error { return nil }}
	if _, err := NewManager(a, b); err == nil {
		t.Fatal("expected duplicate name rejection")
	}
}

func TestTryPublishFailsFastWhenManagerBufferIsFull(t *testing.T) {
	t.Parallel()

	sink := &channelSink{
		ctx: context.Background(), adapter: "meshcore-companion", events: make(chan Event, 1),
	}
	if err := TryPublish(sink, Event{Value: 1}); err != nil {
		t.Fatal(err)
	}
	if err := TryPublish(sink, Event{Value: 2}); !errors.Is(err, ErrEventBufferFull) {
		t.Fatalf("expected buffer-full error, got %v", err)
	}
}

func TestSerialLeaseRegistryPreventsContentionAndReleases(t *testing.T) {
	registry := NewSerialLeaseRegistry()
	release, err := registry.Acquire(" /dev/ttyUSB0 ", "meshtastic")
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if _, err := registry.Acquire("/dev/ttyUSB0", "meshcore-companion"); err == nil {
		t.Fatal("expected lease contention")
	}
	if got := registry.Unleased([]string{"/dev/ttyUSB0", "/dev/ttyACM0"}); len(got) != 1 || got[0] != "/dev/ttyACM0" {
		t.Fatalf("unexpected unleased candidates: %#v", got)
	}
	release()
	release()
	if owner, ok := registry.Owner("/dev/ttyUSB0"); ok || owner != "" {
		t.Fatalf("expected lease released, got owner=%q ok=%v", owner, ok)
	}
	if _, err := registry.Acquire("/dev/ttyUSB0", "meshcore-companion"); err != nil {
		t.Fatalf("reacquire released lease: %v", err)
	}
}

func TestSerialLeaseRegistryCanonicalizesSymlinkAliases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	device := filepath.Join(dir, "ttyACM0")
	if err := os.WriteFile(device, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "by-id-radio")
	if err := os.Symlink(device, alias); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	registry := NewSerialLeaseRegistry()
	release, err := registry.Acquire(alias, "meshtastic")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := registry.Acquire(device, "meshcore-companion"); err == nil {
		t.Fatal("expected canonical device alias lease conflict")
	}
}
