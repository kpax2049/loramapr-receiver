package webportal

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/status"
)

type StatusProvider interface {
	CurrentStatus() status.Snapshot
}

type PairingCodeSubmitter interface {
	SubmitPairingCode(ctx context.Context, code string) error
}

type Server struct {
	addr    string
	status  StatusProvider
	pairing PairingCodeSubmitter
	logger  *slog.Logger
	httpSrv *http.Server
}

func New(addr string, status StatusProvider, pairing PairingCodeSubmitter, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		addr:    addr,
		status:  status,
		pairing: pairing,
		logger:  logger.With("component", "webportal"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/pairing/code", s.handlePairingCode)
	mux.HandleFunc("/", s.handleRoot)
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		err := s.httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	if s.status == nil {
		http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		return
	}
	snapshot := s.status.CurrentStatus()
	if !snapshot.Ready {
		http.Error(w, "not ready: "+snapshot.ReadyReason, http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.status == nil {
		http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		return
	}
	out, err := json.Marshal(s.status.CurrentStatus())
	if err != nil {
		s.logger.Error("status encoding failed", "err", err)
		http.Error(w, "status encoding failed", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(out)
}

func (s *Server) handlePairingCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pairing == nil {
		http.Error(w, "pairing not available", http.StatusServiceUnavailable)
		return
	}

	var request struct {
		PairingCode string `json:"pairingCode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := s.pairing.SubmitPairingCode(r.Context(), request.PairingCode); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	response := map[string]any{
		"accepted": true,
	}
	payload, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "encode response failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(payload)
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	body := "LoRaMapr Receiver local setup portal.\n" +
		"Use /api/status for runtime status, /healthz for liveness, /readyz for readiness.\n" +
		"Submit pairing codes to /api/pairing/code.\n"
	_, _ = w.Write([]byte(body))
}
