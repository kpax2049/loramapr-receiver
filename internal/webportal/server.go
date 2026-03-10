package webportal

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type StatusProvider interface {
	Snapshot() map[string]string
}

type Server struct {
	addr    string
	status  StatusProvider
	httpSrv *http.Server
}

func New(addr string, status StatusProvider) *Server {
	s := &Server{
		addr:   addr,
		status: status,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/", s.handleRoot)
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func (s *Server) Start() error {
	return s.httpSrv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]string{}
	if s.status != nil {
		body = s.status.Snapshot()
	}
	out, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "status encoding failed", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(out)
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("LoRaMapr Receiver setup portal (scaffold)"))
}
