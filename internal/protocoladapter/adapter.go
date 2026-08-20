package protocoladapter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultEventBuffer = 128

type Event struct {
	Adapter    string
	Value      any
	ObservedAt time.Time
}

type AdapterSnapshot struct {
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Transport string    `json:"transport,omitempty"`
	Device    string    `json:"device,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	LastError string    `json:"lastError,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type AdapterSink interface {
	Publish(Event) error
}

type RadioAdapter interface {
	Name() string
	Start(ctx context.Context, sink AdapterSink) error
	Snapshot() AdapterSnapshot
	Close() error
}

type Manager struct {
	mu       sync.RWMutex
	adapters []RadioAdapter
	events   chan Event
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	started  bool
	closed   bool
	errors   map[string]string
}

func NewManager(adapters ...RadioAdapter) (*Manager, error) {
	seen := make(map[string]struct{}, len(adapters))
	copyAdapters := make([]RadioAdapter, 0, len(adapters))
	for _, adapter := range adapters {
		if adapter == nil {
			return nil, errors.New("protocol adapter is nil")
		}
		name := strings.TrimSpace(adapter.Name())
		if name == "" {
			return nil, errors.New("protocol adapter name is required")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate protocol adapter name %q", name)
		}
		seen[name] = struct{}{}
		copyAdapters = append(copyAdapters, adapter)
	}
	return &Manager{
		adapters: copyAdapters,
		events:   make(chan Event, defaultEventBuffer),
		errors:   make(map[string]string),
	}, nil
}

func (m *Manager) Start(ctx context.Context) (<-chan Event, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("protocol adapter manager is closed")
	}
	if m.started {
		events := m.events
		m.mu.Unlock()
		return events, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.started = true
	adapters := append([]RadioAdapter(nil), m.adapters...)
	events := m.events
	m.mu.Unlock()

	for _, adapter := range adapters {
		adapter := adapter
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			sink := &channelSink{ctx: runCtx, adapter: adapter.Name(), events: events}
			if err := adapter.Start(runCtx, sink); err != nil && runCtx.Err() == nil {
				m.mu.Lock()
				m.errors[adapter.Name()] = err.Error()
				m.mu.Unlock()
			}
		}()
	}
	return events, nil
}

func (m *Manager) Snapshots() []AdapterSnapshot {
	m.mu.RLock()
	adapters := append([]RadioAdapter(nil), m.adapters...)
	errs := make(map[string]string, len(m.errors))
	for name, message := range m.errors {
		errs[name] = message
	}
	m.mu.RUnlock()

	result := make([]AdapterSnapshot, 0, len(adapters))
	for _, adapter := range adapters {
		snapshot := adapter.Snapshot()
		if snapshot.Name == "" {
			snapshot.Name = adapter.Name()
		}
		if message := errs[adapter.Name()]; message != "" {
			snapshot.State = "degraded"
			snapshot.LastError = message
			if snapshot.UpdatedAt.IsZero() {
				snapshot.UpdatedAt = time.Now().UTC()
			}
		}
		result = append(result, snapshot)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	cancel := m.cancel
	adapters := append([]RadioAdapter(nil), m.adapters...)
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	var joined error
	for _, adapter := range adapters {
		if err := adapter.Close(); err != nil {
			joined = errors.Join(joined, fmt.Errorf("close adapter %s: %w", adapter.Name(), err))
		}
	}
	m.wg.Wait()
	close(m.events)
	return joined
}

type channelSink struct {
	ctx     context.Context
	adapter string
	events  chan<- Event
}

func (s *channelSink) Publish(event Event) error {
	if strings.TrimSpace(event.Adapter) == "" {
		event.Adapter = s.adapter
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.events <- event:
		return nil
	}
}
