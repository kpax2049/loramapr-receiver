package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var (
	ErrStageFull    = errors.New("outbox stage bound reached")
	ErrEngineClosed = errors.New("outbox writer is closed")
)

type EngineConfig struct {
	StageMaxEvents int
	StageMaxBytes  int64
}

type StageResult struct {
	DeliveryID string
	Err        error
}

type writeOperation struct {
	fn     func(*Store) error
	result chan error
}

type stagedDelivery struct {
	delivery Delivery
	size     int64
}

type Engine struct {
	store *Store

	mu         sync.Mutex
	accepting  bool
	stageBytes int64
	stage      chan stagedDelivery
	operations chan writeOperation
	results    chan StageResult
	done       chan struct{}
	closeOnce  sync.Once
	cfg        EngineConfig
}

func NewEngine(store *Store, cfg EngineConfig) (*Engine, error) {
	if store == nil {
		return nil, errors.New("outbox store is required")
	}
	if cfg.StageMaxEvents <= 0 {
		cfg.StageMaxEvents = DefaultStageMaxEvents
	}
	if cfg.StageMaxBytes <= 0 {
		cfg.StageMaxBytes = DefaultStageMaxBytes
	}
	engine := &Engine{
		store:      store,
		accepting:  true,
		stage:      make(chan stagedDelivery, cfg.StageMaxEvents),
		operations: make(chan writeOperation),
		results:    make(chan StageResult, cfg.StageMaxEvents),
		done:       make(chan struct{}),
		cfg:        cfg,
	}
	go engine.run()
	return engine, nil
}

func (e *Engine) TryStage(delivery Delivery) error {
	copyDelivery := delivery
	copyDelivery.Envelope = append([]byte(nil), delivery.Envelope...)
	encoded, err := json.Marshal(copyDelivery)
	if err != nil {
		return err
	}
	size := int64(len(encoded))

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.accepting {
		return ErrEngineClosed
	}
	if len(e.stage) >= e.cfg.StageMaxEvents || e.stageBytes+size > e.cfg.StageMaxBytes {
		return ErrStageFull
	}
	select {
	case e.stage <- stagedDelivery{delivery: copyDelivery, size: size}:
		e.stageBytes += size
		return nil
	default:
		return ErrStageFull
	}
}

func (e *Engine) Results() <-chan StageResult { return e.results }

func (e *Engine) NextDue(now time.Time) (*Delivery, error) { return e.store.NextDue(now) }
func (e *Engine) Get(deliveryID string) (*Delivery, error) { return e.store.Get(deliveryID) }
func (e *Engine) Stats() (Stats, error)                    { return e.store.Stats() }

func (e *Engine) MarkInflight(deliveryID string) error {
	return e.write(func(store *Store) error { return store.MarkInflight(deliveryID) })
}

func (e *Engine) Retry(deliveryID string, next time.Time, failure AttemptFailure) error {
	return e.write(func(store *Store) error { return store.Retry(deliveryID, next, failure) })
}

func (e *Engine) Quarantine(deliveryID string, reason string, failure AttemptFailure) error {
	return e.write(func(store *Store) error { return store.Quarantine(deliveryID, reason, failure) })
}

func (e *Engine) QuarantineByReceiver(receiverAgentID string, reason string, failure AttemptFailure) (int, error) {
	quarantined := 0
	err := e.write(func(store *Store) error {
		var err error
		quarantined, err = store.QuarantineByReceiver(receiverAgentID, reason, failure)
		return err
	})
	return quarantined, err
}

func (e *Engine) ReconcileBinding(binding Binding) (BindingReconcileResult, error) {
	result := BindingReconcileResult{}
	err := e.write(func(store *Store) error {
		var err error
		result, err = store.ReconcileBinding(binding)
		return err
	})
	return result, err
}

func (e *Engine) Delete(deliveryID string) error {
	return e.write(func(store *Store) error { return store.Delete(deliveryID) })
}

func (e *Engine) PruneQuarantine(now time.Time) (int, error) {
	removed := 0
	err := e.write(func(store *Store) error {
		var err error
		removed, err = store.PruneQuarantine(now)
		return err
	})
	return removed, err
}

func (e *Engine) Close(ctx context.Context) error {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.accepting = false
		close(e.stage)
		e.mu.Unlock()
	})
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) run() {
	defer close(e.done)
	defer close(e.results)
	for {
		select {
		case item, ok := <-e.stage:
			if !ok {
				return
			}
			err := e.store.Enqueue(item.delivery)
			e.mu.Lock()
			e.stageBytes -= item.size
			e.mu.Unlock()
			e.results <- StageResult{DeliveryID: item.delivery.DeliveryID, Err: err}
		case operation := <-e.operations:
			operation.result <- operation.fn(e.store)
		}
	}
}

func (e *Engine) write(fn func(*Store) error) error {
	e.mu.Lock()
	accepting := e.accepting
	e.mu.Unlock()
	if !accepting {
		return ErrEngineClosed
	}
	result := make(chan error, 1)
	select {
	case <-e.done:
		return ErrEngineClosed
	case e.operations <- writeOperation{fn: fn, result: result}:
	}
	select {
	case <-e.done:
		return ErrEngineClosed
	case err := <-result:
		return err
	}
}
