package outbox

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestEngineStagesDurablyAndReportsResult(t *testing.T) {
	store := openTestStore(t, Config{Path: filepath.Join(t.TempDir(), "outbox.db")})
	engine, err := NewEngine(store, EngineConfig{StageMaxEvents: 2, StageMaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	delivery := testDelivery("0198cafe-0000-7000-8000-000000000101", []byte(`{"stage":true}`))
	if err := engine.TryStage(delivery); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-engine.Results():
		if result.DeliveryID != delivery.DeliveryID || result.Err != nil {
			t.Fatalf("unexpected stage result: %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for durable stage result")
	}
	if _, err := engine.Get(delivery.DeliveryID); err != nil {
		t.Fatalf("staged delivery was not durable: %v", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEngineStageBackpressureDoesNotEvict(t *testing.T) {
	store := openTestStore(t, Config{Path: filepath.Join(t.TempDir(), "outbox.db")})
	engine, err := NewEngine(store, EngineConfig{StageMaxEvents: 1, StageMaxBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	delivery := testDelivery("0198cafe-0000-7000-8000-000000000102", []byte(`{"tooLarge":true}`))
	if err := engine.TryStage(delivery); !errors.Is(err, ErrStageFull) {
		t.Fatalf("expected explicit stage full, got %v", err)
	}
	stats, err := store.Stats()
	if err != nil || stats.TotalCount != 0 {
		t.Fatalf("rejected staged item mutated durable store: %#v err=%v", stats, err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestEngineCloseDrainsAcceptedStageEntries(t *testing.T) {
	store := openTestStore(t, Config{Path: filepath.Join(t.TempDir(), "outbox.db")})
	engine, err := NewEngine(store, EngineConfig{StageMaxEvents: 4, StageMaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for index := 0; index < 3; index++ {
		id := []string{
			"0198cafe-0000-7000-8000-000000000103",
			"0198cafe-0000-7000-8000-000000000104",
			"0198cafe-0000-7000-8000-000000000105",
		}[index]
		if err := engine.TryStage(testDelivery(id, []byte(`{"drain":true}`))); err != nil {
			t.Fatal(err)
		}
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	stats, err := store.Stats()
	if err != nil || stats.TotalCount != 3 {
		t.Fatalf("shutdown did not drain accepted stage entries: %#v err=%v", stats, err)
	}
	if err := engine.TryStage(testDelivery("0198cafe-0000-7000-8000-000000000106", []byte(`{}`))); !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("expected closed writer rejection, got %v", err)
	}
}

func TestEnginePrunesQuarantineOnMaintenanceInterval(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	store := openTestStore(t, Config{Path: filepath.Join(t.TempDir(), "outbox.db"), QuarantineRetention: time.Nanosecond, Now: func() time.Time { return now }})
	defer store.Close()
	delivery := testDelivery("0198cafe-0000-7000-8000-000000000107", []byte(`{"prune":true}`))
	if err := store.Enqueue(delivery); err != nil {
		t.Fatal(err)
	}
	if err := store.Quarantine(delivery.DeliveryID, "test", AttemptFailure{}); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(store, EngineConfig{PruneInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		stats, statsErr := engine.Stats()
		if statsErr != nil {
			t.Fatal(statsErr)
		}
		if stats.TotalCount == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hourly-equivalent maintenance did not prune: %#v", stats)
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
