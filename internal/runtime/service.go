package runtime

import (
	"context"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/webportal"
)

type Service struct {
	cfg    config.Config
	store  *state.Store
	cloud  cloudclient.Client
	mesh   meshtastic.Adapter
	portal *webportal.Server
}

func New(cfg config.Config) (*Service, error) {
	if cfg.StoragePath == "" {
		return nil, errors.New("storage path is required")
	}
	if cfg.PortalAddr == "" {
		return nil, errors.New("portal addr is required")
	}
	if cfg.Heartbeat.Std() <= 0 {
		return nil, errors.New("heartbeat interval must be > 0")
	}

	store, err := state.NewStore(filepath.Join(cfg.StoragePath, "state.json"))
	if err != nil {
		return nil, err
	}

	s := &Service{
		cfg:   cfg,
		store: store,
		cloud: cloudclient.NewHTTPClient(cfg.Cloud.BaseURL, cfg.Cloud.APIKey),
		mesh:  meshtastic.NewAdapter(cfg.Meshtastic),
	}
	s.portal = webportal.New(cfg.PortalAddr, s)
	return s, nil
}

func (s *Service) Run(ctx context.Context) error {
	log.Printf("starting loramapr-receiverd node=%s", s.cfg.NodeID)

	packets, err := s.mesh.Start(ctx)
	if err != nil {
		return err
	}

	portalErr := make(chan error, 1)
	go func() {
		portalErr <- s.portal.Start()
	}()

	ticker := time.NewTicker(s.cfg.Heartbeat.Std())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.portal.Shutdown(shutdownCtx)
			return nil
		case err := <-portalErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		case pkt, ok := <-packets:
			if !ok {
				continue
			}
			if err := s.forwardPacket(ctx, pkt); err != nil {
				log.Printf("forward packet failed: %v", err)
				_ = s.store.Update(func(ls *state.LocalState) {
					ls.LastError = err.Error()
				})
			}
		case <-ticker.C:
			if err := s.sendHeartbeat(ctx); err != nil {
				log.Printf("heartbeat failed: %v", err)
				_ = s.store.Update(func(ls *state.LocalState) {
					ls.LastError = err.Error()
				})
			}
		}
	}
}

func (s *Service) forwardPacket(ctx context.Context, pkt meshtastic.Packet) error {
	now := time.Now().UTC()
	err := s.cloud.SendPacket(ctx, cloudclient.Packet{
		Source:    pkt.SourceID,
		Payload:   pkt.Payload,
		Timestamp: pkt.ReceivedAt,
		Meta: map[string]string{
			"adapter": "meshtastic",
		},
	})
	if err != nil {
		return err
	}
	return s.store.Update(func(ls *state.LocalState) {
		ls.LastSeenMesh = &now
		ls.LastError = ""
	})
}

func (s *Service) sendHeartbeat(ctx context.Context) error {
	snap := s.store.Snapshot()
	now := time.Now().UTC()
	err := s.cloud.SendHeartbeat(ctx, cloudclient.Heartbeat{
		NodeID:           s.cfg.NodeID,
		Status:           "running",
		PairingState:     string(snap.PairingStatus),
		ObservedAt:       now,
		MeshtasticHealth: s.mesh.Health(),
	})
	if err != nil {
		return err
	}
	return s.store.Update(func(ls *state.LocalState) {
		ls.LastHeartbeat = &now
		ls.LastError = ""
	})
}

func (s *Service) Snapshot() map[string]string {
	snap := s.store.Snapshot()
	out := map[string]string{
		"node_id":           s.cfg.NodeID,
		"pairing_status":    string(snap.PairingStatus),
		"meshtastic_health": s.mesh.Health(),
		"portal_addr":       s.cfg.PortalAddr,
	}
	if snap.LastError != "" {
		out["last_error"] = snap.LastError
	}
	if snap.LastSeenMesh != nil {
		out["last_seen_mesh"] = snap.LastSeenMesh.Format(time.RFC3339)
	}
	if snap.LastHeartbeat != nil {
		out["last_heartbeat"] = snap.LastHeartbeat.Format(time.RFC3339)
	}
	return out
}
