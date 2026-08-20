package outbox

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	metaBucket       = []byte("meta")
	deliveriesBucket = []byte("deliveries")
	dueIndexBucket   = []byte("due_index")
	quarantineBucket = []byte("quarantine")
	statsBucket      = []byte("stats")

	schemaVersionKey = []byte("schema_version")
	totalCountKey    = []byte("total_count")
	usedBytesKey     = []byte("used_bytes")
	dispatchPauseKey = []byte("dispatch_pause")
)

type Store struct {
	mu                   sync.RWMutex
	db                   *bolt.DB
	cfg                  Config
	recovered            bool
	recoveryCode         string
	maintenanceErrorCode string
	maintenanceError     string
}

func Open(cfg Config) (*Store, error) {
	cfg = withDefaults(cfg)
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, errors.New("outbox path is required")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o750); err != nil {
		return nil, fmt.Errorf("create outbox directory: %w", err)
	}

	existed := fileExists(cfg.Path)
	db, err := bolt.Open(cfg.Path, 0o600, &bolt.Options{Timeout: cfg.LockTimeout, NoSync: false})
	if err != nil {
		if !existed || !recoverableOpenError(err) {
			return nil, fmt.Errorf("open outbox: %w", err)
		}
		return recoverCorrupt(cfg, fmt.Errorf("open outbox: %w", err))
	}
	store := &Store{db: db, cfg: cfg}
	if existed {
		if err := store.checkIntegrity(); err != nil {
			_ = db.Close()
			return recoverCorrupt(cfg, err)
		}
	}
	if err := store.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.resetInflight(); err != nil {
		_ = db.Close()
		return nil, err
	}
	_, _ = store.PruneQuarantine(cfg.Now())
	return store, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Store) Enqueue(input Delivery) error {
	if strings.TrimSpace(input.DeliveryID) == "" || len(input.Envelope) == 0 {
		return errors.New("delivery id and envelope bytes are required")
	}
	now := s.cfg.Now().UTC()
	enqueue := func(tx *bolt.Tx) error {
		deliveries := tx.Bucket(deliveriesBucket)
		quarantine := tx.Bucket(quarantineBucket)
		key := []byte(input.DeliveryID)
		if deliveries.Get(key) != nil || quarantine.Get(key) != nil {
			return ErrDeliveryExists
		}
		sequence, err := deliveries.NextSequence()
		if err != nil {
			return err
		}
		input.State = StatePending
		input.Sequence = sequence
		if input.EnqueuedAt.IsZero() {
			input.EnqueuedAt = now
		} else {
			input.EnqueuedAt = input.EnqueuedAt.UTC()
		}
		if input.NextAttemptAt.IsZero() {
			input.NextAttemptAt = input.EnqueuedAt
		} else {
			input.NextAttemptAt = input.NextAttemptAt.UTC()
		}
		input.Envelope = append([]byte(nil), input.Envelope...)
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		count, used := readCounters(tx)
		if count+1 > s.cfg.MaxEvents || used+int64(len(encoded)) > s.cfg.MaxBytes {
			return ErrOutboxFull
		}
		if err := deliveries.Put(key, encoded); err != nil {
			return err
		}
		if err := tx.Bucket(dueIndexBucket).Put(dueKey(input.NextAttemptAt, sequence, input.DeliveryID), key); err != nil {
			return err
		}
		return writeCounters(tx, count+1, used+int64(len(encoded)))
	}
	err := s.update(enqueue)
	if !errors.Is(err, ErrOutboxFull) {
		return err
	}
	if _, pruneErr := s.PruneQuarantine(now); pruneErr != nil {
		return fmt.Errorf("%w: %v", ErrOutboxPruneFailed, pruneErr)
	}
	return s.update(enqueue)
}

func (s *Store) NextDue(now time.Time) (*Delivery, error) {
	var result *Delivery
	err := s.view(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(dueIndexBucket).Cursor()
		key, deliveryID := cursor.First()
		if key == nil || dueTimestamp(key).After(now.UTC()) {
			return nil
		}
		record, err := decodeDelivery(tx.Bucket(deliveriesBucket).Get(deliveryID))
		if err != nil {
			return err
		}
		result = record
		return nil
	})
	return result, err
}

func (s *Store) MarkInflight(deliveryID string) error {
	return s.updateDelivery(deliveryID, func(record *Delivery) error {
		if record.State != StatePending {
			return fmt.Errorf("delivery %s is not pending", deliveryID)
		}
		record.State = StateInflight
		return nil
	}, false)
}

func (s *Store) Retry(deliveryID string, nextAttemptAt time.Time, failure AttemptFailure) error {
	return s.updateDelivery(deliveryID, func(record *Delivery) error {
		record.State = StatePending
		record.Attempts++
		record.NextAttemptAt = nextAttemptAt.UTC()
		record.LastStatusCode = failure.StatusCode
		record.LastErrorCode = strings.TrimSpace(failure.ErrorCode)
		record.LastError = strings.TrimSpace(failure.Message)
		record.LastRequestID = strings.TrimSpace(failure.RequestID)
		return nil
	}, true)
}

func (s *Store) PauseDelivery(deliveryID string, pause DispatchPause, failure AttemptFailure) error {
	if pause.Kind == "" {
		return errors.New("dispatch pause kind is required")
	}
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(deliveriesBucket)
		key := []byte(deliveryID)
		raw := bucket.Get(key)
		if raw == nil {
			return ErrDeliveryNotFound
		}
		record, err := decodeDelivery(raw)
		if err != nil {
			return err
		}
		oldDueKey := dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID)
		record.State = StatePending
		record.Attempts++
		record.LastStatusCode = failure.StatusCode
		record.LastErrorCode = strings.TrimSpace(failure.ErrorCode)
		record.LastError = strings.TrimSpace(failure.Message)
		record.LastRequestID = strings.TrimSpace(failure.RequestID)
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		pause.DeliveryID = deliveryID
		if pause.PausedAt.IsZero() {
			pause.PausedAt = s.cfg.Now().UTC()
		} else {
			pause.PausedAt = pause.PausedAt.UTC()
		}
		pauseBytes, err := json.Marshal(pause)
		if err != nil {
			return err
		}
		count, used := readCounters(tx)
		if used-int64(len(raw))+int64(len(encoded)) > s.cfg.MaxBytes {
			return ErrOutboxFull
		}
		if err := tx.Bucket(dueIndexBucket).Delete(oldDueKey); err != nil {
			return err
		}
		if err := tx.Bucket(dueIndexBucket).Put(dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID), key); err != nil {
			return err
		}
		if err := bucket.Put(key, encoded); err != nil {
			return err
		}
		if err := tx.Bucket(metaBucket).Put(dispatchPauseKey, pauseBytes); err != nil {
			return err
		}
		return writeCounters(tx, count, used-int64(len(raw))+int64(len(encoded)))
	})
}

func (s *Store) DispatchPause() (*DispatchPause, error) {
	var result *DispatchPause
	err := s.view(func(tx *bolt.Tx) error {
		raw := tx.Bucket(metaBucket).Get(dispatchPauseKey)
		if len(raw) == 0 {
			return nil
		}
		var pause DispatchPause
		if err := json.Unmarshal(raw, &pause); err != nil {
			return fmt.Errorf("decode outbox dispatch pause: %w", err)
		}
		result = &pause
		return nil
	})
	return result, err
}

func (s *Store) ClearResolvedPause(binding Binding) (bool, error) {
	cleared := false
	err := s.update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		raw := meta.Get(dispatchPauseKey)
		if len(raw) == 0 {
			return nil
		}
		var pause DispatchPause
		if err := json.Unmarshal(raw, &pause); err != nil {
			return fmt.Errorf("decode outbox dispatch pause: %w", err)
		}
		switch pause.Kind {
		case DispatchPauseCredential:
			cleared = binding.CredentialGeneration > pause.CredentialGeneration || binding.BindingGeneration > pause.BindingGeneration
		case DispatchPauseBinding:
			cleared = binding.BindingGeneration > pause.BindingGeneration
		case DispatchPauseCollision:
			return nil
		default:
			return fmt.Errorf("unknown outbox dispatch pause kind %q", pause.Kind)
		}
		if cleared {
			return meta.Delete(dispatchPauseKey)
		}
		return nil
	})
	return cleared, err
}

func (s *Store) ResolveDeliveryCollision(deliveryID string) error {
	now := s.cfg.Now().UTC()
	return s.update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		rawPause := meta.Get(dispatchPauseKey)
		if len(rawPause) == 0 {
			return ErrDispatchPauseMismatch
		}
		var pause DispatchPause
		if err := json.Unmarshal(rawPause, &pause); err != nil {
			return fmt.Errorf("decode outbox dispatch pause: %w", err)
		}
		if pause.Kind != DispatchPauseCollision || pause.DeliveryID != deliveryID {
			return ErrDispatchPauseMismatch
		}
		deliveries := tx.Bucket(deliveriesBucket)
		key := []byte(deliveryID)
		raw := deliveries.Get(key)
		if raw == nil {
			return ErrDeliveryNotFound
		}
		record, err := decodeDelivery(raw)
		if err != nil {
			return err
		}
		_ = tx.Bucket(dueIndexBucket).Delete(dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID))
		record.State = StateQuarantined
		record.QuarantinedAt = now
		record.QuarantineReason = "delivery_id_collision_operator_resolved"
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		count, used := readCounters(tx)
		if used-int64(len(raw))+int64(len(encoded)) > s.cfg.MaxBytes {
			return ErrOutboxFull
		}
		if err := tx.Bucket(quarantineBucket).Put(key, encoded); err != nil {
			return err
		}
		if err := deliveries.Delete(key); err != nil {
			return err
		}
		if err := meta.Delete(dispatchPauseKey); err != nil {
			return err
		}
		return writeCounters(tx, count, used-int64(len(raw))+int64(len(encoded)))
	})
}

func (s *Store) Quarantine(deliveryID string, reason string, failure AttemptFailure) error {
	now := s.cfg.Now().UTC()
	return s.update(func(tx *bolt.Tx) error {
		deliveries := tx.Bucket(deliveriesBucket)
		key := []byte(deliveryID)
		raw := deliveries.Get(key)
		if raw == nil {
			return ErrDeliveryNotFound
		}
		record, err := decodeDelivery(raw)
		if err != nil {
			return err
		}
		oldLen := len(raw)
		_ = tx.Bucket(dueIndexBucket).Delete(dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID))
		record.State = StateQuarantined
		record.QuarantinedAt = now
		record.QuarantineReason = strings.TrimSpace(reason)
		record.LastStatusCode = failure.StatusCode
		record.LastErrorCode = strings.TrimSpace(failure.ErrorCode)
		record.LastError = strings.TrimSpace(failure.Message)
		record.LastRequestID = strings.TrimSpace(failure.RequestID)
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		count, used := readCounters(tx)
		if used-int64(oldLen)+int64(len(encoded)) > s.cfg.MaxBytes {
			return ErrOutboxFull
		}
		if err := tx.Bucket(quarantineBucket).Put(key, encoded); err != nil {
			return err
		}
		if err := deliveries.Delete(key); err != nil {
			return err
		}
		return writeCounters(tx, count, used-int64(oldLen)+int64(len(encoded)))
	})
}

func (s *Store) QuarantineByReceiver(receiverAgentID string, reason string, failure AttemptFailure) (int, error) {
	receiverAgentID = strings.TrimSpace(receiverAgentID)
	if receiverAgentID == "" {
		return 0, errors.New("receiver agent id is required")
	}
	now := s.cfg.Now().UTC()
	quarantined := 0
	err := s.update(func(tx *bolt.Tx) error {
		deliveries := tx.Bucket(deliveriesBucket)
		quarantine := tx.Bucket(quarantineBucket)
		type matched struct {
			key    []byte
			raw    []byte
			record *Delivery
		}
		matches := make([]matched, 0)
		if err := deliveries.ForEach(func(key, raw []byte) error {
			record, err := decodeDelivery(raw)
			if err != nil {
				return err
			}
			if record.ReceiverAgentIDSnapshot == receiverAgentID {
				matches = append(matches, matched{
					key: append([]byte(nil), key...), raw: append([]byte(nil), raw...), record: record,
				})
			}
			return nil
		}); err != nil {
			return err
		}

		count, used := readCounters(tx)
		for _, item := range matches {
			record := item.record
			_ = tx.Bucket(dueIndexBucket).Delete(dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID))
			record.State = StateQuarantined
			record.QuarantinedAt = now
			record.QuarantineReason = strings.TrimSpace(reason)
			record.LastStatusCode = failure.StatusCode
			record.LastErrorCode = strings.TrimSpace(failure.ErrorCode)
			record.LastError = strings.TrimSpace(failure.Message)
			encoded, err := json.Marshal(record)
			if err != nil {
				return err
			}
			used = used - int64(len(item.raw)) + int64(len(encoded))
			if used > s.cfg.MaxBytes {
				return ErrOutboxFull
			}
			if err := quarantine.Put(item.key, encoded); err != nil {
				return err
			}
			if err := deliveries.Delete(item.key); err != nil {
				return err
			}
			quarantined++
		}
		return writeCounters(tx, count, used)
	})
	return quarantined, err
}

// ReconcileBinding prevents a pending immutable delivery from ever being sent
// with credentials for a different authenticated principal. Ordinary API-key
// rotation preserves deliveries because the key itself is not part of this
// binding; receiver-agent or installation replacement quarantines old bytes.
func (s *Store) ReconcileBinding(binding Binding) (BindingReconcileResult, error) {
	binding.OwnerID = strings.TrimSpace(binding.OwnerID)
	binding.ReceiverAgentID = strings.TrimSpace(binding.ReceiverAgentID)
	binding.InstallationID = strings.TrimSpace(binding.InstallationID)
	if binding.OwnerID == "" || binding.ReceiverAgentID == "" || binding.InstallationID == "" {
		return BindingReconcileResult{}, errors.New("complete outbox binding is required")
	}

	now := s.cfg.Now().UTC()
	result := BindingReconcileResult{}
	err := s.update(func(tx *bolt.Tx) error {
		deliveries := tx.Bucket(deliveriesBucket)
		quarantine := tx.Bucket(quarantineBucket)
		type matched struct {
			key    []byte
			raw    []byte
			record *Delivery
			reason string
		}
		matches := make([]matched, 0)
		if err := deliveries.ForEach(func(key, raw []byte) error {
			record, err := decodeDelivery(raw)
			if err != nil {
				return err
			}
			reason := ""
			switch {
			case record.InstallationID != binding.InstallationID:
				reason = "installation_reset"
			case record.OwnerID != binding.OwnerID || record.ReceiverAgentIDSnapshot != binding.ReceiverAgentID:
				reason = "credential_rebind_required"
			default:
				result.Kept++
			}
			if reason != "" {
				matches = append(matches, matched{
					key: append([]byte(nil), key...), raw: append([]byte(nil), raw...), record: record, reason: reason,
				})
			}
			return nil
		}); err != nil {
			return err
		}

		count, used := readCounters(tx)
		for _, item := range matches {
			record := item.record
			_ = tx.Bucket(dueIndexBucket).Delete(dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID))
			record.State = StateQuarantined
			record.QuarantinedAt = now
			record.QuarantineReason = item.reason
			record.LastErrorCode = "OUTBOX_BINDING_CHANGED"
			record.LastError = "pending normalized delivery binding no longer matches active receiver credentials"
			encoded, err := json.Marshal(record)
			if err != nil {
				return err
			}
			used = used - int64(len(item.raw)) + int64(len(encoded))
			if used > s.cfg.MaxBytes {
				return ErrOutboxFull
			}
			if err := quarantine.Put(item.key, encoded); err != nil {
				return err
			}
			if err := deliveries.Delete(item.key); err != nil {
				return err
			}
			if item.reason == "installation_reset" {
				result.InstallationResetQuarantined++
			} else {
				result.CredentialRebindQuarantined++
			}
		}
		return writeCounters(tx, count, used)
	})
	return result, err
}

func (s *Store) Delete(deliveryID string) error {
	return s.update(func(tx *bolt.Tx) error {
		key := []byte(deliveryID)
		for _, bucketName := range [][]byte{deliveriesBucket, quarantineBucket} {
			bucket := tx.Bucket(bucketName)
			raw := bucket.Get(key)
			if raw == nil {
				continue
			}
			if sameBucket(bucketName, deliveriesBucket) {
				record, err := decodeDelivery(raw)
				if err != nil {
					return err
				}
				_ = tx.Bucket(dueIndexBucket).Delete(dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID))
			}
			count, used := readCounters(tx)
			if err := bucket.Delete(key); err != nil {
				return err
			}
			return writeCounters(tx, count-1, used-int64(len(raw)))
		}
		return ErrDeliveryNotFound
	})
}

func (s *Store) Get(deliveryID string) (*Delivery, error) {
	var result *Delivery
	err := s.view(func(tx *bolt.Tx) error {
		key := []byte(deliveryID)
		for _, bucketName := range [][]byte{deliveriesBucket, quarantineBucket} {
			raw := tx.Bucket(bucketName).Get(key)
			if raw == nil {
				continue
			}
			record, err := decodeDelivery(raw)
			if err != nil {
				return err
			}
			result = record
			return nil
		}
		return ErrDeliveryNotFound
	})
	return result, err
}

func (s *Store) PruneQuarantine(now time.Time) (int, error) {
	removed := 0
	err := s.update(func(tx *bolt.Tx) error {
		var err error
		removed, err = pruneQuarantineTx(tx, now, s.cfg.QuarantineRetention)
		return err
	})
	s.mu.Lock()
	if err != nil {
		s.maintenanceErrorCode = "outbox_prune_failed"
		s.maintenanceError = err.Error()
	} else {
		s.maintenanceErrorCode = ""
		s.maintenanceError = ""
	}
	s.mu.Unlock()
	return removed, err
}

func pruneQuarantineTx(tx *bolt.Tx, now time.Time, retention time.Duration) (int, error) {
	removed := 0
	bucket := tx.Bucket(quarantineBucket)
	cursor := bucket.Cursor()
	count, used := readCounters(tx)
	cutoff := now.UTC().Add(-retention)
	for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
		record, err := decodeDelivery(raw)
		if err != nil {
			return 0, err
		}
		if record.QuarantinedAt.IsZero() || record.QuarantinedAt.After(cutoff) {
			continue
		}
		if err := cursor.Delete(); err != nil {
			return 0, err
		}
		count--
		used -= int64(len(raw))
		removed++
	}
	return removed, writeCounters(tx, count, used)
}

func (s *Store) Stats() (Stats, error) {
	s.mu.RLock()
	result := Stats{Recovered: s.recovered, RecoveryCode: s.recoveryCode, MaintenanceErrorCode: s.maintenanceErrorCode, MaintenanceError: s.maintenanceError}
	s.mu.RUnlock()
	err := s.view(func(tx *bolt.Tx) error {
		if raw := tx.Bucket(metaBucket).Get(dispatchPauseKey); len(raw) > 0 {
			var pause DispatchPause
			if err := json.Unmarshal(raw, &pause); err != nil {
				return fmt.Errorf("decode outbox dispatch pause: %w", err)
			}
			result.DispatchPause = &pause
		}
		result.TotalCount, result.UsedBytes = readCounters(tx)
		result.PendingCount = tx.Bucket(deliveriesBucket).Stats().KeyN
		result.QuarantinedCount = tx.Bucket(quarantineBucket).Stats().KeyN
		if err := tx.Bucket(deliveriesBucket).ForEach(func(_, raw []byte) error {
			record, err := decodeDelivery(raw)
			if err != nil {
				return err
			}
			if result.OldestPendingAt == nil || record.EnqueuedAt.Before(*result.OldestPendingAt) {
				oldest := record.EnqueuedAt
				result.OldestPendingAt = &oldest
			}
			return nil
		}); err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func (s *Store) initialize() error {
	return s.update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{metaBucket, deliveriesBucket, dueIndexBucket, quarantineBucket, statsBucket} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		meta := tx.Bucket(metaBucket)
		stored := meta.Get(schemaVersionKey)
		if stored != nil {
			version, err := strconv.Atoi(string(stored))
			if err != nil || version != SchemaVersion {
				return fmt.Errorf("%w: %q", ErrUnknownSchema, string(stored))
			}
		} else if err := meta.Put(schemaVersionKey, []byte(strconv.Itoa(SchemaVersion))); err != nil {
			return err
		}
		return rebuildCounters(tx)
	})
}

func (s *Store) resetInflight() error {
	now := s.cfg.Now().UTC()
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(deliveriesBucket)
		type persisted struct {
			key    []byte
			record *Delivery
		}
		records := make([]persisted, 0, bucket.Stats().KeyN)
		if err := bucket.ForEach(func(key, raw []byte) error {
			record, err := decodeDelivery(raw)
			if err != nil {
				return err
			}
			records = append(records, persisted{key: append([]byte(nil), key...), record: record})
			return nil
		}); err != nil {
			return err
		}
		if err := tx.DeleteBucket(dueIndexBucket); err != nil {
			return err
		}
		due, err := tx.CreateBucket(dueIndexBucket)
		if err != nil {
			return err
		}
		for _, item := range records {
			record := item.record
			if record.State == StateInflight {
				record.State = StatePending
				record.NextAttemptAt = now
			}
			if record.State != StatePending {
				return fmt.Errorf("delivery %s has invalid durable state %q", record.DeliveryID, record.State)
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if err := bucket.Put(item.key, encoded); err != nil {
				return err
			}
			if err := due.Put(dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID), item.key); err != nil {
				return err
			}
		}
		return rebuildCounters(tx)
	})
}

func (s *Store) updateDelivery(deliveryID string, mutate func(*Delivery) error, makeDue bool) error {
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(deliveriesBucket)
		key := []byte(deliveryID)
		raw := bucket.Get(key)
		if raw == nil {
			return ErrDeliveryNotFound
		}
		record, err := decodeDelivery(raw)
		if err != nil {
			return err
		}
		oldDueKey := dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID)
		if err := mutate(record); err != nil {
			return err
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		count, used := readCounters(tx)
		if used-int64(len(raw))+int64(len(encoded)) > s.cfg.MaxBytes {
			return ErrOutboxFull
		}
		if err := tx.Bucket(dueIndexBucket).Delete(oldDueKey); err != nil {
			return err
		}
		if makeDue {
			if err := tx.Bucket(dueIndexBucket).Put(dueKey(record.NextAttemptAt, record.Sequence, record.DeliveryID), key); err != nil {
				return err
			}
		}
		if err := bucket.Put(key, encoded); err != nil {
			return err
		}
		return writeCounters(tx, count, used-int64(len(raw))+int64(len(encoded)))
	})
}

func (s *Store) checkIntegrity() error {
	return s.db.View(func(tx *bolt.Tx) error {
		var first error
		for err := range tx.Check() {
			if err != nil && first == nil {
				first = err
			}
		}
		if first != nil {
			return fmt.Errorf("outbox integrity check failed: %w", first)
		}
		return nil
	})
}

func (s *Store) update(fn func(*bolt.Tx) error) error {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return errors.New("outbox is closed")
	}
	return db.Update(fn)
}

func (s *Store) view(fn func(*bolt.Tx) error) error {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return errors.New("outbox is closed")
	}
	return db.View(fn)
}

func recoverCorrupt(cfg Config, cause error) (*Store, error) {
	backup := fmt.Sprintf("%s.corrupt-%s", cfg.Path, cfg.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.Rename(cfg.Path, backup); err != nil {
		return nil, fmt.Errorf("preserve corrupt outbox after %v: %w", cause, err)
	}
	if err := syncDirectory(filepath.Dir(cfg.Path)); err != nil {
		return nil, fmt.Errorf("sync corrupt outbox preservation: %w", err)
	}
	db, err := bolt.Open(cfg.Path, 0o600, &bolt.Options{Timeout: cfg.LockTimeout, NoSync: false})
	if err != nil {
		return nil, fmt.Errorf("create replacement outbox after %v: %w", cause, err)
	}
	store := &Store{db: db, cfg: cfg, recovered: true, recoveryCode: "outbox_recovered_from_corruption"}
	if err := store.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.resetInflight(); err != nil {
		_ = db.Close()
		return nil, err
	}
	_, _ = store.PruneQuarantine(cfg.Now())
	return store, nil
}

func withDefaults(cfg Config) Config {
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = DefaultMaxEvents
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	if cfg.QuarantineRetention <= 0 {
		cfg.QuarantineRetention = DefaultQuarantineRetention
	}
	if cfg.LockTimeout <= 0 {
		cfg.LockTimeout = time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return cfg
}

func dueKey(at time.Time, sequence uint64, deliveryID string) []byte {
	key := make([]byte, 16+len(deliveryID))
	binary.BigEndian.PutUint64(key[:8], uint64(at.UTC().UnixNano()))
	binary.BigEndian.PutUint64(key[8:16], sequence)
	copy(key[16:], deliveryID)
	return key
}

func dueTimestamp(key []byte) time.Time {
	if len(key) < 8 {
		return time.Time{}
	}
	return time.Unix(0, int64(binary.BigEndian.Uint64(key[:8]))).UTC()
}

func decodeDelivery(raw []byte) (*Delivery, error) {
	if len(raw) == 0 {
		return nil, ErrDeliveryNotFound
	}
	var record Delivery
	if err := json.Unmarshal(append([]byte(nil), raw...), &record); err != nil {
		return nil, fmt.Errorf("decode outbox delivery: %w", err)
	}
	record.Envelope = append([]byte(nil), record.Envelope...)
	return &record, nil
}

func readCounters(tx *bolt.Tx) (int, int64) {
	bucket := tx.Bucket(statsBucket)
	return int(readUint64(bucket.Get(totalCountKey))), int64(readUint64(bucket.Get(usedBytesKey)))
}

func writeCounters(tx *bolt.Tx, count int, used int64) error {
	if count < 0 || used < 0 {
		return errors.New("outbox counters underflow")
	}
	bucket := tx.Bucket(statsBucket)
	if err := bucket.Put(totalCountKey, uint64Bytes(uint64(count))); err != nil {
		return err
	}
	return bucket.Put(usedBytesKey, uint64Bytes(uint64(used)))
}

func rebuildCounters(tx *bolt.Tx) error {
	count := 0
	used := int64(0)
	for _, name := range [][]byte{deliveriesBucket, quarantineBucket} {
		if err := tx.Bucket(name).ForEach(func(_, raw []byte) error {
			count++
			used += int64(len(raw))
			return nil
		}); err != nil {
			return err
		}
	}
	return writeCounters(tx, count, used)
}

func uint64Bytes(value uint64) []byte {
	result := make([]byte, 8)
	binary.BigEndian.PutUint64(result, value)
	return result
}

func readUint64(value []byte) uint64 {
	if len(value) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(value)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func recoverableOpenError(err error) bool {
	return errors.Is(err, bolt.ErrInvalid) ||
		errors.Is(err, bolt.ErrInvalidMapping) ||
		errors.Is(err, bolt.ErrVersionMismatch) ||
		errors.Is(err, bolt.ErrChecksum)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

func sameBucket(left []byte, right []byte) bool { return bytes.Equal(left, right) }
