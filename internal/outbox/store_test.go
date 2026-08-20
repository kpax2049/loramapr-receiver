package outbox

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestStorePersistsExactBytesAndRecoversInflight(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "ingest-outbox.db")
	store := openTestStore(t, Config{Path: path, Now: func() time.Time { return now }})

	delayed := testDelivery("0198cafe-0000-7000-8000-000000000001", []byte(`{"z":2,"a":1}`))
	delayed.NextAttemptAt = now.Add(time.Hour)
	if err := store.Enqueue(delayed); err != nil {
		t.Fatalf("enqueue delayed: %v", err)
	}
	ready := testDelivery("0198cafe-0000-7000-8000-000000000002", []byte("{\n  \"exact\": true\n}"))
	if err := store.Enqueue(ready); err != nil {
		t.Fatalf("enqueue ready: %v", err)
	}

	next, err := store.NextDue(now)
	if err != nil {
		t.Fatalf("next due: %v", err)
	}
	if next == nil || next.DeliveryID != ready.DeliveryID {
		t.Fatalf("retry-delayed head blocked ready record: %#v", next)
	}
	if !bytes.Equal(next.Envelope, ready.Envelope) {
		t.Fatalf("exact envelope bytes changed: %q", next.Envelope)
	}
	if err := store.MarkInflight(ready.DeliveryID); err != nil {
		t.Fatalf("mark inflight: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	now = now.Add(time.Minute)
	reopened := openTestStore(t, Config{Path: path, Now: func() time.Time { return now }})
	defer reopened.Close()
	recovered, err := reopened.NextDue(now)
	if err != nil {
		t.Fatalf("next due after restart: %v", err)
	}
	if recovered == nil || recovered.DeliveryID != ready.DeliveryID || recovered.State != StatePending {
		t.Fatalf("inflight record was not recovered pending: %#v", recovered)
	}
	if !bytes.Equal(recovered.Envelope, ready.Envelope) {
		t.Fatal("restart changed immutable envelope bytes")
	}
}

func TestStoreRetryPreservesImmutableDelivery(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	store := openTestStore(t, Config{Path: filepath.Join(t.TempDir(), "outbox.db"), Now: func() time.Time { return now }})
	defer store.Close()
	delivery := testDelivery("0198cafe-0000-7000-8000-000000000003", []byte(`{"immutable":true}`))
	if err := store.Enqueue(delivery); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInflight(delivery.DeliveryID); err != nil {
		t.Fatal(err)
	}
	if err := store.Retry(delivery.DeliveryID, now.Add(time.Minute), AttemptFailure{
		StatusCode: 503,
		ErrorCode:  "RECEIVER_EVENTS_V1_DISABLED",
		Message:    "retry later",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(delivery.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeliveryID != delivery.DeliveryID || got.IdempotencyKey != delivery.IdempotencyKey ||
		got.EnvelopeSHA256 != delivery.EnvelopeSHA256 || !bytes.Equal(got.Envelope, delivery.Envelope) {
		t.Fatalf("retry mutated immutable delivery: %#v", got)
	}
	if got.Attempts != 1 || got.LastStatusCode != 503 || got.State != StatePending {
		t.Fatalf("retry metadata not persisted: %#v", got)
	}
}

func TestStoreBoundsNeverEvictPending(t *testing.T) {
	store := openTestStore(t, Config{
		Path:      filepath.Join(t.TempDir(), "outbox.db"),
		MaxEvents: 1,
		MaxBytes:  1 << 20,
	})
	defer store.Close()
	first := testDelivery("0198cafe-0000-7000-8000-000000000004", []byte(`{"first":true}`))
	second := testDelivery("0198cafe-0000-7000-8000-000000000005", []byte(`{"second":true}`))
	if err := store.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(second); !errors.Is(err, ErrOutboxFull) {
		t.Fatalf("expected full error, got %v", err)
	}
	if _, err := store.Get(first.DeliveryID); err != nil {
		t.Fatalf("existing pending delivery was evicted: %v", err)
	}
	stats, err := store.Stats()
	if err != nil || stats.TotalCount != 1 {
		t.Fatalf("unexpected bounded stats: %#v err=%v", stats, err)
	}
}

func TestStoreQuarantineRetention(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	store := openTestStore(t, Config{Path: filepath.Join(t.TempDir(), "outbox.db"), Now: func() time.Time { return now }})
	defer store.Close()
	delivery := testDelivery("0198cafe-0000-7000-8000-000000000006", []byte(`{"bad":true}`))
	if err := store.Enqueue(delivery); err != nil {
		t.Fatal(err)
	}
	if err := store.Quarantine(delivery.DeliveryID, "invalid_event", AttemptFailure{StatusCode: 422, ErrorCode: "INVALID_EVENT"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(delivery.DeliveryID)
	if err != nil || got.State != StateQuarantined || got.QuarantineReason != "invalid_event" {
		t.Fatalf("unexpected quarantine record: %#v err=%v", got, err)
	}
	if next, err := store.NextDue(now.Add(time.Hour)); err != nil || next != nil {
		t.Fatalf("quarantined delivery remained dispatchable: %#v err=%v", next, err)
	}
	removed, err := store.PruneQuarantine(now.Add(DefaultQuarantineRetention - time.Second))
	if err != nil || removed != 0 {
		t.Fatalf("quarantine pruned early: removed=%d err=%v", removed, err)
	}
	removed, err = store.PruneQuarantine(now.Add(DefaultQuarantineRetention + time.Second))
	if err != nil || removed != 1 {
		t.Fatalf("expired quarantine not pruned: removed=%d err=%v", removed, err)
	}
}

func TestStoreRecoversCorruptFileWithoutSilentOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ingest-outbox.db")
	if err := os.WriteFile(path, []byte("not-a-bbolt-database"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 123, time.UTC)
	store := openTestStore(t, Config{Path: path, Now: func() time.Time { return now }})
	defer store.Close()
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Recovered || stats.RecoveryCode != "outbox_recovered_from_corruption" {
		t.Fatalf("corruption recovery not surfaced: %#v", stats)
	}
	backups, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("corrupt database was not preserved: %#v err=%v", backups, err)
	}
	preserved, err := os.ReadFile(backups[0])
	if err != nil || string(preserved) != "not-a-bbolt-database" {
		t.Fatalf("corrupt bytes not preserved exactly: %q err=%v", preserved, err)
	}
}

func TestStoreUnknownSchemaFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucket(metaBucket)
		if err != nil {
			return err
		}
		return bucket.Put(schemaVersionKey, []byte("99"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Path: path}); !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("expected unknown schema failure, got %v", err)
	}
	if backups, _ := filepath.Glob(path + ".corrupt-*"); len(backups) != 0 {
		t.Fatalf("unknown future schema was incorrectly treated as corruption: %#v", backups)
	}
}

func TestStoreLockContentionFailsWithoutRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	first := openTestStore(t, Config{Path: path})
	defer first.Close()
	if _, err := Open(Config{Path: path, LockTimeout: 10 * time.Millisecond}); err == nil {
		t.Fatal("expected lock contention failure")
	}
	if backups, _ := filepath.Glob(path + ".corrupt-*"); len(backups) != 0 {
		t.Fatalf("lock contention was incorrectly treated as corruption: %#v", backups)
	}
}

func TestStoreTransactionRollbackAndClosedWriteDoNotCreateDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	store := openTestStore(t, Config{Path: path})
	sentinel := errors.New("interrupted")
	if err := store.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(deliveriesBucket).Put([]byte("partial"), []byte("partial")); err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("unexpected rollback error: %v", err)
	}
	if rawErr := store.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(deliveriesBucket).Get([]byte("partial")) != nil {
			return errors.New("rolled-back delivery persisted")
		}
		return nil
	}); rawErr != nil {
		t.Fatal(rawErr)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(testDelivery("0198cafe-0000-7000-8000-000000000007", []byte(`{}`))); err == nil {
		t.Fatal("expected closed store write failure")
	}
	reopened := openTestStore(t, Config{Path: path})
	defer reopened.Close()
	stats, err := reopened.Stats()
	if err != nil || stats.TotalCount != 0 {
		t.Fatalf("failed writes changed durable state: %#v err=%v", stats, err)
	}
}

func TestStoreRejectsDuplicateDeliveryID(t *testing.T) {
	store := openTestStore(t, Config{Path: filepath.Join(t.TempDir(), "outbox.db")})
	defer store.Close()
	delivery := testDelivery("0198cafe-0000-7000-8000-000000000008", []byte(`{"same":true}`))
	if err := store.Enqueue(delivery); err != nil {
		t.Fatal(err)
	}
	changed := delivery
	changed.Envelope = []byte(`{"same":false}`)
	if err := store.Enqueue(changed); !errors.Is(err, ErrDeliveryExists) {
		t.Fatalf("expected duplicate id rejection, got %v", err)
	}
	got, err := store.Get(delivery.DeliveryID)
	if err != nil || !bytes.Equal(got.Envelope, delivery.Envelope) {
		t.Fatalf("duplicate enqueue mutated durable bytes: %#v err=%v", got, err)
	}
}

func openTestStore(t *testing.T, cfg Config) *Store {
	t.Helper()
	store, err := Open(cfg)
	if err != nil {
		t.Fatalf("open outbox: %v", err)
	}
	return store
}

func testDelivery(id string, envelope []byte) Delivery {
	return Delivery{
		DeliveryID:              id,
		Envelope:                append([]byte(nil), envelope...),
		EnvelopeSHA256:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IdempotencyKey:          id,
		OwnerID:                 "owner-1",
		ReceiverAgentIDSnapshot: "11111111-1111-4111-8111-111111111111",
		InstallationID:          "0123456789abcdef0123456789abcdef",
		Endpoint:                "/api/receiver/events/v1",
	}
}
