package meshtastic

import (
	"context"
	"errors"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/config"
)

type Packet struct {
	SourceID   string
	Payload    []byte
	ReceivedAt time.Time
}

type Adapter interface {
	Start(ctx context.Context) (<-chan Packet, error)
	Health() string
}

type StubAdapter struct {
	cfg config.MeshConfig
}

func NewAdapter(cfg config.MeshConfig) Adapter {
	return &StubAdapter{cfg: cfg}
}

func (a *StubAdapter) Start(ctx context.Context) (<-chan Packet, error) {
	if a.cfg.Transport == "" {
		return nil, errors.New("meshtastic transport not configured")
	}
	out := make(chan Packet)
	go func() {
		<-ctx.Done()
		close(out)
	}()
	return out, nil
}

func (a *StubAdapter) Health() string {
	if a.cfg.Transport == "" {
		return "not_configured"
	}
	return "idle"
}
