package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/pairing"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
	"github.com/loramapr/loramapr-receiver/internal/webportal"
)

type Service struct {
	container *Container
	mode      config.RunMode
}

type Container struct {
	Config     config.Config
	Logger     *slog.Logger
	State      *state.Store
	Status     *status.Model
	Pairing    *pairing.Manager
	Meshtastic meshtastic.Adapter
	MeshEvents <-chan meshtastic.Event
	Portal     *webportal.Server
}

func New(cfg config.Config, logger *slog.Logger) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}

	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		return nil, fmt.Errorf("open state store: %w", err)
	}

	current := store.Snapshot()
	mode := resolveMode(cfg.Service.Mode, current.Pairing.Phase)
	profile := detectRuntimeProfile(cfg.Paths.StateFile)

	if err := store.Update(func(data *state.Data) {
		data.Installation.LastStartedAt = time.Now().UTC()
		data.Cloud.EndpointURL = cfg.Cloud.BaseURL
		data.Runtime.Profile = profile
		data.Runtime.Mode = string(mode)
	}); err != nil {
		return nil, fmt.Errorf("persist startup state: %w", err)
	}

	current = store.Snapshot()
	statusModel := status.New()
	statusModel.SetInstallationID(current.Installation.ID)
	statusModel.SetMode(string(mode))
	statusModel.SetRuntimeProfile(profile)
	statusModel.SetPairingPhase(string(current.Pairing.Phase))
	statusModel.SetCloud(cfg.Cloud.BaseURL, "unknown")
	statusModel.SetComponent("runtime", "starting", "initializing service container")
	statusModel.SetComponent("portal", "stopped", "portal not started yet")

	svc := &Service{mode: mode}
	cloud := cloudclient.NewHTTPClient(cfg.Cloud.BaseURL, 10*time.Second)
	mesh := meshtastic.NewAdapter(cfg.Meshtastic, logger.With("component", "meshtastic"))
	meshSnap := mesh.Snapshot()
	statusModel.SetComponent("meshtastic", string(meshSnap.State), meshtasticStatusMessage(meshSnap))

	svc.container = &Container{
		Config:     cfg,
		Logger:     logger.With("component", "runtime"),
		State:      store,
		Status:     statusModel,
		Meshtastic: mesh,
		Pairing: pairing.NewManager(
			store,
			statusModel,
			cloud,
			logger,
			pairing.ActivationIdentity{
				RuntimeVersion: "0.1.0-dev",
			},
		),
	}
	svc.container.Portal = webportal.New(cfg.Portal.BindAddress, svc, svc, logger.With("component", "webportal"))
	svc.configureInitialReadiness(current.Pairing.Phase)
	return svc, nil
}

func (s *Service) Run(ctx context.Context) error {
	c := s.container
	c.Logger.Info(
		"starting loramapr-receiverd",
		"mode", s.mode,
		"profile", c.Status.Snapshot().RuntimeProfile,
		"state_file", c.State.Path(),
		"portal_bind", c.Config.Portal.BindAddress,
	)

	c.Status.SetLifecycle(status.LifecycleRunning)
	c.Status.SetComponent("runtime", "running", "runtime loop active")
	c.Status.SetComponent("portal", "starting", "local setup portal starting")

	meshEvents, err := c.Meshtastic.Start(ctx)
	if err != nil {
		return err
	}
	c.MeshEvents = meshEvents

	portalErr := make(chan error, 1)
	go func() {
		portalErr <- c.Portal.Run(ctx)
	}()

	ticker := time.NewTicker(c.Config.Service.Heartbeat.Std())
	defer ticker.Stop()
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			c.Logger.Info("shutdown requested")
			c.Status.SetLifecycle(status.LifecycleStopping)
			c.Status.SetReady(false, "shutdown requested")
			c.Status.SetComponent("runtime", "stopping", "context canceled")
			c.Status.SetLifecycle(status.LifecycleStopped)
			return nil
		case err := <-portalErr:
			if err != nil {
				c.Logger.Error("portal failed", "err", err)
				c.Status.SetLastError(err.Error())
				c.Status.SetLifecycle(status.LifecycleFailed)
				c.Status.SetComponent("portal", "error", "portal terminated unexpectedly")
				c.Status.SetReady(false, "local portal failed")
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("portal exited unexpectedly")
		case event, ok := <-c.MeshEvents:
			if !ok {
				c.Logger.Warn("meshtastic event stream closed")
				c.Status.SetComponent("meshtastic", "degraded", "meshtastic stream closed")
				continue
			}
			s.onMeshtasticEvent(event)
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) CurrentStatus() status.Snapshot {
	return s.container.Status.Snapshot()
}

func (s *Service) SubmitPairingCode(ctx context.Context, code string) error {
	return s.container.Pairing.SubmitPairingCode(ctx, code)
}

func (s *Service) StatusModel() *status.Model {
	return s.container.Status
}

func (s *Service) StateStore() *state.Store {
	return s.container.State
}

func (s *Service) Mode() config.RunMode {
	return s.mode
}

func (s *Service) tick(ctx context.Context) {
	c := s.container
	if err := c.Pairing.Process(ctx); err != nil {
		c.Logger.Error("pairing lifecycle tick failed", "err", err)
		c.Status.SetLastError("pairing tick failed")
	}

	snap := c.State.Snapshot()
	c.Status.SetPairingPhase(string(snap.Pairing.Phase))
	c.Status.SetCloud(c.Config.Cloud.BaseURL, pairingCloudStatus(snap.Pairing.Phase))
	meshSnap := c.Meshtastic.Snapshot()
	c.Status.SetComponent("meshtastic", string(meshSnap.State), meshtasticStatusMessage(meshSnap))

	switch s.mode {
	case config.ModeSetup:
		c.Status.SetReady(true, "setup portal available")
		if snap.Pairing.Phase == state.PairingSteadyState {
			c.Status.SetComponent("service", "ready", "receiver paired and activated")
		} else {
			c.Status.SetComponent("service", "setup", "receiver in setup mode")
		}
	case config.ModeService:
		ready := snap.Pairing.Phase == state.PairingSteadyState
		if ready {
			c.Status.SetReady(true, "service mode active")
		} else {
			c.Status.SetReady(false, "service mode requires paired state")
		}
		c.Status.SetComponent("pairing", string(snap.Pairing.Phase), "pairing state from persistent store")
		c.Status.SetComponent("service", "running", "service mode loop active")
	default:
		c.Status.SetReady(false, "unknown runtime mode")
		c.Status.SetLastError("unknown runtime mode")
	}
}

func (s *Service) onMeshtasticEvent(event meshtastic.Event) {
	c := s.container
	switch event.Kind {
	case meshtastic.EventPacket:
		if event.Packet != nil {
			c.Status.SetComponent(
				"meshtastic",
				"connected",
				fmt.Sprintf("packet from %s port=%d", event.Packet.SourceNodeID, event.Packet.PortNum),
			)
		}
	case meshtastic.EventStatus:
		if event.Node != nil {
			c.Status.SetComponent(
				"meshtastic",
				"connected",
				fmt.Sprintf("local node %s observed=%d", event.Node.LocalNodeID, len(event.Node.ObservedNodeIDs)),
			)
		}
	}
}

func (s *Service) configureInitialReadiness(phase state.PairingPhase) {
	switch s.mode {
	case config.ModeSetup:
		s.container.Status.SetReady(true, "setup mode selected")
	case config.ModeService:
		if phase == state.PairingSteadyState {
			s.container.Status.SetReady(true, "paired state present")
			return
		}
		s.container.Status.SetReady(false, "service mode requested without steady pairing state")
	default:
		s.container.Status.SetReady(false, "runtime mode unresolved")
	}
}

func resolveMode(requested config.RunMode, pairingPhase state.PairingPhase) config.RunMode {
	switch requested {
	case config.ModeSetup:
		return config.ModeSetup
	case config.ModeService:
		return config.ModeService
	default:
		if pairingPhase == state.PairingSteadyState {
			return config.ModeService
		}
		return config.ModeSetup
	}
}

func detectRuntimeProfile(stateFile string) string {
	path := strings.ToLower(filepath.Clean(stateFile))
	switch {
	case strings.HasPrefix(path, "/var/lib/loramapr"):
		return "linux-service"
	case strings.Contains(path, "appdata"):
		return "windows-user"
	default:
		return "local-dev"
	}
}

func pairingCloudStatus(phase state.PairingPhase) string {
	switch phase {
	case state.PairingSteadyState, state.PairingActivated:
		return "credential_ready"
	case state.PairingBootstrapExchanged:
		return "activating"
	case state.PairingCodeEntered:
		return "pairing"
	default:
		return "unknown"
	}
}

func meshtasticStatusMessage(snapshot meshtastic.Snapshot) string {
	device := snapshot.DetectedDevice
	if device == "" {
		device = snapshot.Device
	}
	if device == "" {
		device = "none"
	}
	message := fmt.Sprintf("device=%s packets=%d observed_nodes=%d", device, snapshot.PacketsSeen, len(snapshot.ObservedNodeIDs))
	if snapshot.LocalNodeID != "" {
		message += " local_node=" + snapshot.LocalNodeID
	}
	if snapshot.LastError != "" {
		message += " error=" + snapshot.LastError
	}
	return message
}
