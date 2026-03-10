package webportal

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/status"
)

//go:embed templates/*.tmpl
var portalTemplateFiles embed.FS

type StatusProvider interface {
	CurrentStatus() status.Snapshot
}

type PairingCodeSubmitter interface {
	SubmitPairingCode(ctx context.Context, code string) error
}

type Server struct {
	addr      string
	status    StatusProvider
	pairing   PairingCodeSubmitter
	logger    *slog.Logger
	templates map[string]*template.Template
	httpSrv   *http.Server
}

type pageData struct {
	Title                string
	Snapshot             status.Snapshot
	Flash                string
	FlashClass           string
	SummaryHint          string
	SummaryHintClass     string
	NextAction           string
	MeshtasticState      string
	TroubleshootingHints []string
	RuntimeVersion       string
	GoVersion            string
	Platform             string
	Arch                 string
}

func New(addr string, statusProvider StatusProvider, pairing PairingCodeSubmitter, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	templates, err := loadTemplates()
	if err != nil {
		panic(err)
	}

	s := &Server{
		addr:      addr,
		status:    statusProvider,
		pairing:   pairing,
		logger:    logger.With("component", "webportal"),
		templates: templates,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/pairing/code", s.handlePairingCode)
	mux.HandleFunc("/pairing", s.routePairing)
	mux.HandleFunc("/progress", s.handleProgress)
	mux.HandleFunc("/troubleshooting", s.handleTroubleshooting)
	mux.HandleFunc("/advanced", s.handleAdvanced)
	mux.HandleFunc("/", s.handleWelcome)

	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func (s *Server) Handler() http.Handler {
	return s.httpSrv.Handler
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
	payload, err := json.Marshal(s.status.CurrentStatus())
	if err != nil {
		s.logger.Error("status encoding failed", "err", err)
		http.Error(w, "status encoding failed", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(payload)
}

func (s *Server) handlePairingCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		PairingCode string `json:"pairingCode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := s.submitPairingCode(r.Context(), request.PairingCode); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	response := map[string]any{"accepted": true}
	payload, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "encode response failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(payload)
}

func (s *Server) routePairing(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.renderPairing(w, r, "", "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			s.renderPairing(w, r, "invalid form payload", "err")
			return
		}
		if err := s.submitPairingCode(r.Context(), r.Form.Get("pairing_code")); err != nil {
			s.renderPairing(w, r, err.Error(), "err")
			return
		}
		http.Redirect(w, r, "/progress?submitted=1", http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWelcome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	snap := s.currentSnapshot()
	data := s.basePageData("Welcome", snap)
	data.SummaryHint, data.SummaryHintClass = summaryHint(snap)
	data.NextAction = nextAction(snap)
	s.renderHTML(w, http.StatusOK, "welcome", data)
}

func (s *Server) renderPairing(w http.ResponseWriter, _ *http.Request, flash, flashClass string) {
	snap := s.currentSnapshot()
	data := s.basePageData("Pairing", snap)
	data.Flash = flash
	data.FlashClass = flashClass
	s.renderHTML(w, http.StatusOK, "pairing", data)
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	snap := s.currentSnapshot()
	data := s.basePageData("Progress", snap)
	if r.URL.Query().Get("submitted") == "1" {
		data.Flash = "Pairing code submitted. Progress updates will appear here."
		data.FlashClass = "ok"
	}
	data.MeshtasticState = componentState(snap, "meshtastic")
	s.renderHTML(w, http.StatusOK, "progress", data)
}

func (s *Server) handleTroubleshooting(w http.ResponseWriter, _ *http.Request) {
	snap := s.currentSnapshot()
	data := s.basePageData("Troubleshooting", snap)
	data.TroubleshootingHints = troubleshootingHints(snap)
	s.renderHTML(w, http.StatusOK, "troubleshooting", data)
}

func (s *Server) handleAdvanced(w http.ResponseWriter, _ *http.Request) {
	snap := s.currentSnapshot()
	data := s.basePageData("Advanced", snap)
	s.renderHTML(w, http.StatusOK, "advanced", data)
}

func (s *Server) submitPairingCode(ctx context.Context, code string) error {
	if s.pairing == nil {
		return errors.New("pairing subsystem is not available")
	}
	return s.pairing.SubmitPairingCode(ctx, code)
}

func (s *Server) currentSnapshot() status.Snapshot {
	if s.status == nil {
		return status.Snapshot{}
	}
	return s.status.CurrentStatus()
}

func (s *Server) basePageData(title string, snap status.Snapshot) pageData {
	return pageData{
		Title:          title,
		Snapshot:       snap,
		RuntimeVersion: runtimeVersion(),
		GoVersion:      goruntime.Version(),
		Platform:       goruntime.GOOS,
		Arch:           goruntime.GOARCH,
	}
}

func (s *Server) renderHTML(w http.ResponseWriter, statusCode int, name string, data pageData) {
	tmpl, ok := s.templates[name]
	if !ok {
		http.Error(w, "portal template missing", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("render template failed", "name", name, "err", err)
	}
}

func loadTemplates() (map[string]*template.Template, error) {
	pages := []string{"welcome", "pairing", "progress", "troubleshooting", "advanced"}
	out := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		parsed, err := template.ParseFS(
			portalTemplateFiles,
			"templates/layout.tmpl",
			fmt.Sprintf("templates/%s.tmpl", page),
		)
		if err != nil {
			return nil, err
		}
		out[page] = parsed
	}
	return out, nil
}

func componentState(snap status.Snapshot, component string) string {
	if snap.Components == nil {
		return "unknown"
	}
	entry, ok := snap.Components[component]
	if !ok {
		return "unknown"
	}
	if entry.State == "" {
		return "unknown"
	}
	return entry.State
}

func summaryHint(snap status.Snapshot) (string, string) {
	switch snap.PairingPhase {
	case "unpaired":
		return "Receiver is waiting for a pairing code.", "warn"
	case "pairing_code_entered", "bootstrap_exchanged":
		return "Pairing is in progress. Keep this page open and wait for activation.", "warn"
	case "activated", "steady_state":
		return "Receiver is paired and credentials are active.", "ok"
	default:
		if snap.LastError != "" {
			return "Receiver reported an error. Check troubleshooting details.", "err"
		}
		return "Receiver status is initializing.", "warn"
	}
}

func nextAction(snap status.Snapshot) string {
	switch snap.PairingPhase {
	case "unpaired":
		return "Open Pairing and enter the pairing code from LoRaMapr Cloud."
	case "pairing_code_entered", "bootstrap_exchanged":
		return "Monitor Progress until the receiver reaches steady state."
	case "activated", "steady_state":
		return "Verify meshtastic node detection and packet forwarding in Progress."
	default:
		return "Check current status and resolve any reported errors."
	}
}

func troubleshootingHints(snap status.Snapshot) []string {
	hints := []string{}
	if snap.PairingPhase == "unpaired" {
		hints = append(hints, "Generate a fresh pairing code in LoRaMapr Cloud and submit it on the Pairing page.")
	}
	if snap.CloudStatus == "unknown" {
		hints = append(hints, "If cloud status stays unknown, verify internet connectivity and cloud base URL configuration.")
	}
	if !snap.Ready {
		reason := strings.TrimSpace(snap.ReadyReason)
		if reason == "" {
			reason = "no readiness reason provided"
		}
		hints = append(hints, "Runtime is not ready: "+reason)
	}
	meshtasticState := componentState(snap, "meshtastic")
	if meshtasticState == "not_present" {
		hints = append(hints, "No local Meshtastic device detected yet. Check USB connection and device permissions.")
	}
	if meshtasticState == "degraded" {
		hints = append(hints, "Meshtastic adapter is degraded. Verify configured device path and input stream format.")
	}
	if snap.LastError != "" {
		hints = append(hints, "Last runtime error: "+snap.LastError)
	}
	if len(hints) == 0 {
		hints = append(hints, "No active issues detected. Continue monitoring Progress for node and ingest updates.")
	}
	return hints
}

func runtimeVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
