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
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/buildinfo"
	"github.com/loramapr/loramapr-receiver/internal/diagnostics"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

//go:embed templates/*.tmpl
var portalTemplateFiles embed.FS

type StatusProvider interface {
	CurrentStatus() status.Snapshot
}

type PairingCodeSubmitter interface {
	SubmitPairingCode(ctx context.Context, code string) error
	ResetPairing(ctx context.Context, deauthorize bool) error
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
	Attention            diagnostics.Attention
	Flash                string
	FlashClass           string
	SummaryHint          string
	SummaryHintClass     string
	NextAction           string
	MeshtasticState      string
	NetworkState         string
	TroubleshootingHints []string
	OperationalOverall   string
	OperationalSummary   string
	OperationalChecks    []diagnostics.OperationalCheck
	RuntimeVersion       string
	ReleaseChannel       string
	BuildCommit          string
	BuildDate            string
	BuildID              string
	GoVersion            string
	Platform             string
	Arch                 string
	InstallType          string
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
	mux.HandleFunc("/api/ops", s.handleOps)
	mux.HandleFunc("/api/pairing/code", s.handlePairingCode)
	mux.HandleFunc("/api/lifecycle/reset", s.handleLifecycleReset)
	mux.HandleFunc("/pairing", s.routePairing)
	mux.HandleFunc("/reset", s.routeReset)
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

func (s *Server) handleOps(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.status == nil {
		http.Error(w, "status unavailable", http.StatusServiceUnavailable)
		return
	}
	snap := s.status.CurrentStatus()
	ops := evaluateOperationalFromSnapshot(snap)
	payload, err := json.Marshal(struct {
		diagnostics.OperationalSummary
		Attention diagnostics.Attention `json:"attention"`
	}{
		OperationalSummary: ops,
		Attention:          deriveAttentionFromSnapshot(snap),
	})
	if err != nil {
		s.logger.Error("ops encoding failed", "err", err)
		http.Error(w, "ops encoding failed", http.StatusInternalServerError)
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

func (s *Server) handleLifecycleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pairing == nil {
		http.Error(w, "pairing subsystem is not available", http.StatusServiceUnavailable)
		return
	}

	request := struct {
		Deauthorize *bool `json:"deauthorize,omitempty"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	deauthorize := true
	if request.Deauthorize != nil {
		deauthorize = *request.Deauthorize
	}
	if err := s.pairing.ResetPairing(r.Context(), deauthorize); err != nil {
		http.Error(w, "reset failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	response := map[string]any{
		"accepted":    true,
		"deauthorize": deauthorize,
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

func (s *Server) routePairing(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		flash := ""
		flashClass := ""
		if r.URL.Query().Get("reset") == "1" {
			flash = "Receiver reset completed. Enter a new pairing code to link this installation."
			flashClass = "ok"
		}
		s.renderPairing(w, r, flash, flashClass)
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

func (s *Server) routeReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pairing == nil {
		http.Error(w, "pairing subsystem is not available", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form payload", http.StatusBadRequest)
		return
	}

	deauthorize := parseDeauthorizeValue(r.Form.Get("deauthorize"), true)
	if err := s.pairing.ResetPairing(r.Context(), deauthorize); err != nil {
		http.Error(w, "reset failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/pairing?reset=1", http.StatusSeeOther)
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
	data.NetworkState = componentState(snap, "network")
	ops := evaluateOperationalFromSnapshot(snap)
	data.OperationalOverall = ops.Overall
	data.OperationalSummary = ops.Summary
	data.OperationalChecks = append([]diagnostics.OperationalCheck(nil), ops.Checks...)
	s.renderHTML(w, http.StatusOK, "progress", data)
}

func (s *Server) handleTroubleshooting(w http.ResponseWriter, _ *http.Request) {
	snap := s.currentSnapshot()
	data := s.basePageData("Troubleshooting", snap)
	data.TroubleshootingHints = troubleshootingHints(snap)
	ops := evaluateOperationalFromSnapshot(snap)
	data.OperationalOverall = ops.Overall
	data.OperationalSummary = ops.Summary
	data.OperationalChecks = append([]diagnostics.OperationalCheck(nil), ops.Checks...)
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
	build := buildinfo.Current()
	version := snap.ReceiverVersion
	if version == "" {
		version = build.Version
	}
	channel := snap.ReleaseChannel
	if channel == "" {
		channel = build.Channel
	}
	commit := snap.BuildCommit
	if commit == "" {
		commit = build.Commit
	}
	buildDate := snap.BuildDate
	if buildDate == "" {
		buildDate = build.BuildDate
	}
	buildID := snap.BuildID
	if buildID == "" {
		buildID = build.BuildID
	}
	platform := snap.Platform
	if platform == "" {
		platform = goruntime.GOOS
	}
	arch := snap.Arch
	if arch == "" {
		arch = goruntime.GOARCH
	}
	return pageData{
		Title:          title,
		Snapshot:       snap,
		Attention:      deriveAttentionFromSnapshot(snap),
		RuntimeVersion: version,
		ReleaseChannel: channel,
		BuildCommit:    commit,
		BuildDate:      buildDate,
		BuildID:        buildID,
		GoVersion:      goruntime.Version(),
		Platform:       platform,
		Arch:           arch,
		InstallType:    snap.InstallType,
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
	attention := deriveAttentionFromSnapshot(snap)
	switch attention.State {
	case diagnostics.AttentionUrgent:
		summary := strings.TrimSpace(attention.Summary)
		if summary == "" {
			summary = "Receiver requires immediate operator attention."
		}
		if hint := strings.TrimSpace(attention.Hint); hint != "" {
			summary += " " + hint
		}
		return summary, "err"
	case diagnostics.AttentionActionRequired:
		summary := strings.TrimSpace(attention.Summary)
		if summary == "" {
			summary = "Receiver requires action to recover normal operation."
		}
		if hint := strings.TrimSpace(attention.Hint); hint != "" {
			summary += " " + hint
		}
		return summary, "warn"
	case diagnostics.AttentionInfo:
		summary := strings.TrimSpace(attention.Summary)
		if summary != "" {
			if hint := strings.TrimSpace(attention.Hint); hint != "" {
				summary += " " + hint
			}
			return summary, "warn"
		}
	}

	if strings.TrimSpace(snap.FailureCode) != "" {
		summary := strings.TrimSpace(snap.FailureSummary)
		if summary == "" {
			summary = "Receiver reported failure state: " + snap.FailureCode
		}
		hint := strings.TrimSpace(snap.FailureHint)
		if hint != "" {
			summary += " " + hint
		}
		return summary, "err"
	}

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
	attention := deriveAttentionFromSnapshot(snap)
	isAppliance := strings.EqualFold(strings.TrimSpace(snap.RuntimeProfile), "appliance-pi")
	switch attention.Code {
	case "operational_blocked":
		return "Open Troubleshooting and resolve blocking checks before expecting cloud forwarding."
	case "operational_degraded":
		return "Review Progress operational checks and resolve warnings before they become service issues."
	}
	switch attention.Category {
	case diagnostics.AttentionCategoryLifecycle:
		return "Reset and re-pair this receiver to restore an active cloud identity."
	case diagnostics.AttentionCategoryAuthorization:
		return "Receiver authorization is invalid. Re-pair to issue fresh durable credentials."
	case diagnostics.AttentionCategoryCompatibility:
		return "Resolve runtime/config compatibility first, then restart receiver and confirm ready state."
	case diagnostics.AttentionCategoryVersion:
		return "Upgrade receiver to the recommended supported version/channel."
	case diagnostics.AttentionCategoryConnectivity:
		return "Restore local network/cloud connectivity, then confirm heartbeat and forwarding recover."
	case diagnostics.AttentionCategoryNode:
		return "Connect a Meshtastic node and verify the adapter reaches connected state."
	case diagnostics.AttentionCategoryForwarding:
		return "Verify cloud reachability and auth, then confirm queued packets are acknowledged."
	}

	switch strings.TrimSpace(snap.FailureCode) {
	case "receiver_credential_revoked", "receiver_disabled", "receiver_replaced":
		return "Use reset/re-pair to relink this receiver with LoRaMapr Cloud."
	}
	switch snap.PairingPhase {
	case "unpaired":
		if isAppliance {
			return "From another LAN device, open http://loramapr-receiver.local:8080 (or the Pi IP address) and enter your pairing code."
		}
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
	isAppliance := strings.EqualFold(strings.TrimSpace(snap.RuntimeProfile), "appliance-pi")
	attention := deriveAttentionFromSnapshot(snap)
	if attention.State != diagnostics.AttentionNone {
		stateLabel := strings.TrimSpace(string(attention.State))
		if stateLabel == "" {
			stateLabel = "attention"
		}
		summary := strings.TrimSpace(attention.Summary)
		if summary != "" {
			hints = append(hints, "Attention state ("+stateLabel+"): "+summary)
		}
		if hint := strings.TrimSpace(attention.Hint); hint != "" {
			hints = append(hints, "Recommended next step: "+hint)
		}
	}
	if strings.TrimSpace(snap.FailureCode) != "" {
		summary := strings.TrimSpace(snap.FailureSummary)
		if summary == "" {
			summary = snap.FailureCode
		}
		hints = append(hints, "Current failure: "+summary)
		if hint := strings.TrimSpace(snap.FailureHint); hint != "" {
			hints = append(hints, "Suggested action: "+hint)
		}
	}
	switch strings.TrimSpace(snap.FailureCode) {
	case "receiver_credential_revoked", "receiver_disabled", "receiver_replaced":
		hints = append(hints, "Lifecycle recovery: use reset to clear local credentials, then submit a fresh pairing code.")
	case "receiver_version_unsupported":
		hints = append(hints, "Receiver build is unsupported and should be upgraded before continued operation.")
	case "receiver_outdated":
		hints = append(hints, "Receiver is outdated. Upgrade is recommended to stay aligned with release policy.")
	case "local_schema_incompatible":
		hints = append(hints, "Local config/state schema is incompatible with this runtime. Upgrade receiver binary or restore compatible schema.")
	}
	switch strings.TrimSpace(snap.UpdateStatus) {
	case "outdated":
		hints = append(hints, "Receiver is outdated. Plan an upgrade to the recommended release shown on Progress/Advanced pages.")
	case "channel_mismatch":
		hints = append(hints, "Receiver channel differs from manifest channel. Confirm intended rollout channel for this installation.")
	case "unsupported":
		hints = append(hints, "Installed receiver version is unsupported for current policy. Upgrade is required for continued support.")
	}
	if snap.PairingPhase == "unpaired" {
		hints = append(hints, "Generate a fresh pairing code in LoRaMapr Cloud and submit it on the Pairing page.")
	}
	if isAppliance {
		hints = append(hints, "Appliance discovery: try http://loramapr-receiver.local:8080 first, then fallback to the Pi LAN IP.")
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
	networkState := componentState(snap, "network")
	if networkState == "unavailable" {
		hints = append(hints, "Network is unavailable. Confirm Ethernet/Wi-Fi setup and DHCP address assignment before pairing.")
	}
	if networkState == "unknown" && isAppliance {
		hints = append(hints, "Network probe state is unknown. Wait for boot completion, then refresh this page.")
	}
	if snap.LastError != "" {
		hints = append(hints, "Last runtime error: "+snap.LastError)
	}
	for _, recent := range snap.RecentFailures {
		if recent.Code == "" || recent.Summary == "" {
			continue
		}
		hints = append(hints, fmt.Sprintf("Recent failure [%s]: %s", recent.Code, recent.Summary))
	}
	if len(hints) == 0 {
		hints = append(hints, "No active issues detected. Continue monitoring Progress for node and ingest updates.")
	}
	return hints
}

func evaluateOperationalFromSnapshot(snap status.Snapshot) diagnostics.OperationalSummary {
	return diagnostics.EvaluateOperational(diagnostics.OperationalInput{
		Now:                 time.Now().UTC(),
		Lifecycle:           string(snap.Lifecycle),
		Ready:               snap.Ready,
		ReadyReason:         snap.ReadyReason,
		PairingPhase:        snap.PairingPhase,
		HasIngestCredential: strings.TrimSpace(snap.PairingPhase) == "steady_state",
		CloudReachable:      snap.CloudReachable,
		CloudProbeStatus:    snap.CloudStatus,
		MeshtasticState:     componentState(snap, "meshtastic"),
		IngestQueueDepth:    snap.IngestQueueDepth,
		LastPacketQueued:    snap.LastPacketQueued,
		LastPacketAck:       snap.LastPacketAck,
		UpdateStatus:        snap.UpdateStatus,
	})
}

func deriveAttentionFromSnapshot(snap status.Snapshot) diagnostics.Attention {
	explicitState := diagnostics.AttentionState(strings.TrimSpace(snap.AttentionState))
	if explicitState != "" {
		attention := diagnostics.Attention{
			State:          explicitState,
			Category:       diagnostics.AttentionCategory(strings.TrimSpace(snap.AttentionCategory)),
			Code:           strings.TrimSpace(snap.AttentionCode),
			Summary:        strings.TrimSpace(snap.AttentionSummary),
			Hint:           strings.TrimSpace(snap.AttentionHint),
			ActionRequired: snap.AttentionActionRequired,
		}
		if attention.State == diagnostics.AttentionNone {
			return diagnostics.Attention{State: diagnostics.AttentionNone}
		}
		if attention.Summary != "" || attention.Code != "" {
			return attention
		}
	}

	finding := diagnostics.Finding{
		Code:    diagnostics.FailureCode(strings.TrimSpace(snap.FailureCode)),
		Summary: strings.TrimSpace(snap.FailureSummary),
		Hint:    strings.TrimSpace(snap.FailureHint),
	}
	return diagnostics.DeriveAttention(finding, evaluateOperationalFromSnapshot(snap))
}

func parseDeauthorizeValue(raw string, defaultValue bool) bool {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "", "default":
		return defaultValue
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}
