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
	"strconv"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/buildinfo"
	"github.com/loramapr/loramapr-receiver/internal/config"
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

type HomeAutoSessionManager interface {
	CurrentHomeAutoSessionConfig() config.HomeAutoSessionConfig
	UpdateHomeAutoSessionConfig(ctx context.Context, cfg config.HomeAutoSessionConfig) error
	ReevaluateHomeAutoSession(ctx context.Context) error
	ResetHomeAutoSession(ctx context.Context) error
}

type Server struct {
	addr      string
	status    StatusProvider
	pairing   PairingCodeSubmitter
	homeAuto  HomeAutoSessionManager
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
	SetupIssues          []diagnostics.SetupIssue
	RuntimeVersion       string
	ReleaseChannel       string
	BuildCommit          string
	BuildDate            string
	BuildID              string
	GoVersion            string
	Platform             string
	Arch                 string
	InstallType          string
	HomeAutoConfig       config.HomeAutoSessionConfig
	HomeAutoTrackedText  string
	HomeAutoStartDeb     string
	HomeAutoStopDeb      string
	HomeAutoIdleTimeout  string
	HomeAutoStateHint    string
	HomeAutoConfigHint   string
}

func New(addr string, statusProvider StatusProvider, pairing PairingCodeSubmitter, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	templates, err := loadTemplates()
	if err != nil {
		panic(err)
	}

	var homeAuto HomeAutoSessionManager
	if manager, ok := pairing.(HomeAutoSessionManager); ok {
		homeAuto = manager
	}

	s := &Server{
		addr:      addr,
		status:    statusProvider,
		pairing:   pairing,
		homeAuto:  homeAuto,
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
	mux.HandleFunc("/home-auto-session", s.routeHomeAutoSession)
	mux.HandleFunc("/home-auto-session/reevaluate", s.handleHomeAutoReevaluate)
	mux.HandleFunc("/home-auto-session/reset", s.handleHomeAutoReset)
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
	setupIssues := diagnostics.DeriveSetupIssues(snap, ops)
	payload, err := json.Marshal(struct {
		diagnostics.OperationalSummary
		Attention   diagnostics.Attention    `json:"attention"`
		SetupIssues []diagnostics.SetupIssue `json:"setup_issues,omitempty"`
	}{
		OperationalSummary: ops,
		Attention:          deriveAttentionFromSnapshot(snap),
		SetupIssues:        append([]diagnostics.SetupIssue(nil), setupIssues...),
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

func (s *Server) routeHomeAutoSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		flash := ""
		flashClass := ""
		if r.URL.Query().Get("reeval") == "1" {
			flash = "Home Auto Session reevaluate requested."
			flashClass = "ok"
		}
		if r.URL.Query().Get("reset") == "1" {
			flash = "Home Auto Session degraded/cooldown state reset."
			flashClass = "ok"
		}
		s.renderHomeAutoSession(w, flash, flashClass)
	case http.MethodPost:
		if s.homeAuto == nil {
			http.Error(w, "home auto session module is not available", http.StatusServiceUnavailable)
			return
		}
		if err := r.ParseForm(); err != nil {
			s.renderHomeAutoSession(w, "invalid form payload", "err")
			return
		}
		cfg, err := parseHomeAutoSessionConfigForm(r)
		if err != nil {
			s.renderHomeAutoSession(w, err.Error(), "err")
			return
		}
		if err := s.homeAuto.UpdateHomeAutoSessionConfig(r.Context(), cfg); err != nil {
			s.renderHomeAutoSession(w, "save failed: "+err.Error(), "err")
			return
		}
		if err := s.homeAuto.ReevaluateHomeAutoSession(r.Context()); err != nil {
			s.renderHomeAutoSession(w, "saved, but reevaluate failed: "+err.Error(), "warn")
			return
		}
		s.renderHomeAutoSession(w, "Home Auto Session configuration saved.", "ok")
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHomeAutoReevaluate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.homeAuto == nil {
		http.Error(w, "home auto session module is not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.homeAuto.ReevaluateHomeAutoSession(r.Context()); err != nil {
		http.Error(w, "reevaluate failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/home-auto-session?reeval=1", http.StatusSeeOther)
}

func (s *Server) handleHomeAutoReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.homeAuto == nil {
		http.Error(w, "home auto session module is not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.homeAuto.ResetHomeAutoSession(r.Context()); err != nil {
		http.Error(w, "reset failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/home-auto-session?reset=1", http.StatusSeeOther)
}

func (s *Server) renderHomeAutoSession(w http.ResponseWriter, flash, flashClass string) {
	snap := s.currentSnapshot()
	data := s.basePageData("Home Auto Session", snap)
	data.Flash = flash
	data.FlashClass = flashClass
	if s.homeAuto != nil {
		cfg := s.homeAuto.CurrentHomeAutoSessionConfig()
		data.HomeAutoConfig = cfg
		data.HomeAutoTrackedText = strings.Join(cfg.TrackedNodeIDs, ", ")
		data.HomeAutoStartDeb = cfg.StartDebounce.Std().String()
		data.HomeAutoStopDeb = cfg.StopDebounce.Std().String()
		data.HomeAutoIdleTimeout = cfg.IdleStopTimeout.Std().String()
	} else {
		data.HomeAutoTrackedText = ""
		data.HomeAutoStartDeb = ""
		data.HomeAutoStopDeb = ""
		data.HomeAutoIdleTimeout = ""
	}
	data.HomeAutoStateHint = homeAutoStateHint(snap)
	data.HomeAutoConfigHint = homeAutoConfigHint(snap)
	s.renderHTML(w, http.StatusOK, "home_auto_session", data)
}

func (s *Server) handleWelcome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	snap := s.currentSnapshot()
	data := s.basePageData("Welcome", snap)
	ops := evaluateOperationalFromSnapshot(snap)
	data.SetupIssues = diagnostics.DeriveSetupIssues(snap, ops)
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
	data.SetupIssues = diagnostics.DeriveSetupIssues(snap, ops)
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
	data.SetupIssues = diagnostics.DeriveSetupIssues(snap, ops)
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
	pages := []string{"welcome", "pairing", "progress", "home_auto_session", "troubleshooting", "advanced"}
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
		return "Receiver is installed and waiting for a pairing code.", "warn"
	case "pairing_code_entered", "bootstrap_exchanged":
		return "Pairing is in progress. Keep this page open while the receiver activates.", "warn"
	case "activated", "steady_state":
		return "Receiver is paired and ready for normal forwarding.", "ok"
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
		return "Open Troubleshooting and resolve blocking checks before expecting normal forwarding."
	case "operational_degraded":
		return "Review Progress checks and resolve warnings before they become service issues."
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
		return "Verify cloud connectivity and authorization, then confirm queued packets are acknowledged."
	}

	switch strings.TrimSpace(snap.FailureCode) {
	case "receiver_credential_revoked", "receiver_disabled", "receiver_replaced":
		if strings.TrimSpace(snap.CloudReceiverLabel) != "" {
			return "This receiver identity is no longer active (" + strings.TrimSpace(snap.CloudReceiverLabel) + "). Use reset/re-pair to relink."
		}
		return "Use reset/re-pair to relink this receiver with LoRaMapr Cloud."
	}
	switch snap.PairingPhase {
	case "unpaired":
		if strings.TrimSpace(snap.CloudReceiverID) == "" {
			return "Open Pairing and submit a code from LoRaMapr Cloud."
		}
		if isAppliance {
			return "From another LAN device, open http://loramapr-receiver.local:8080 (or the Pi IP address) and enter your pairing code."
		}
		return "Open Pairing and enter the pairing code from LoRaMapr Cloud."
	case "pairing_code_entered", "bootstrap_exchanged":
		return "Monitor Progress until pairing completes and receiver shows ready."
	case "activated", "steady_state":
		return "Verify Meshtastic node connection and packet forwarding on Progress."
	default:
		return "Check current status and resolve any reported errors."
	}
}

func troubleshootingHints(snap status.Snapshot) []string {
	hints := []string{}
	isAppliance := strings.EqualFold(strings.TrimSpace(snap.RuntimeProfile), "appliance-pi")
	ops := evaluateOperationalFromSnapshot(snap)
	for _, issue := range diagnostics.DeriveSetupIssues(snap, ops) {
		summary := strings.TrimSpace(issue.Summary)
		if summary == "" {
			continue
		}
		message := "Setup root cause [" + strings.TrimSpace(issue.Code) + "]: " + summary
		if guidance := strings.TrimSpace(issue.Guidance); guidance != "" {
			message += " Next: " + guidance
		}
		hints = append(hints, message)
	}
	if strings.TrimSpace(snap.LocalName) != "" {
		hints = append(hints, "Local receiver name: "+strings.TrimSpace(snap.LocalName))
	}
	if strings.TrimSpace(snap.CloudReceiverLabel) != "" || strings.TrimSpace(snap.CloudReceiverID) != "" {
		receiver := strings.TrimSpace(snap.CloudReceiverLabel)
		if receiver == "" {
			receiver = strings.TrimSpace(snap.CloudReceiverID)
		}
		hints = append(hints, "Cloud receiver identity: "+receiver)
	}
	if strings.TrimSpace(snap.CloudSiteLabel) != "" || strings.TrimSpace(snap.CloudGroupLabel) != "" {
		site := strings.TrimSpace(snap.CloudSiteLabel)
		if site == "" {
			site = "not set"
		}
		group := strings.TrimSpace(snap.CloudGroupLabel)
		if group == "" {
			group = "not set"
		}
		hints = append(hints, fmt.Sprintf("Optional cloud labels: site=%s group=%s", site, group))
	}
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
		hints = append(hints, "Current issue: "+summary)
		if hint := strings.TrimSpace(snap.FailureHint); hint != "" {
			hints = append(hints, "Suggested next step: "+hint)
		}
	}
	switch strings.TrimSpace(snap.FailureCode) {
	case "receiver_credential_revoked", "receiver_disabled", "receiver_replaced":
		hints = append(hints, "Lifecycle recovery: use reset to clear local credentials, then submit a fresh pairing code.")
		if strings.TrimSpace(snap.FailureCode) == "receiver_replaced" {
			hints = append(hints, "This receiver was superseded by another receiver. Re-pair only if this host should become active again.")
		}
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
		hints = append(hints, "Pairing links this local installation directly; it does not require workspace/site/group setup in the portal.")
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
		if snap.PairingPhase == "steady_state" {
			hints = append(hints, "In multi-receiver setups, confirm the Meshtastic device is physically attached to this receiver and not another host.")
		}
	}
	if meshtasticState == "degraded" {
		hints = append(hints, "Meshtastic adapter is degraded. Verify configured device path, USB serial permissions, and native protocol connectivity.")
	}
	meshCfg := snap.MeshtasticConfig
	if meshCfg.Available {
		parts := []string{}
		if value := strings.TrimSpace(meshCfg.Region); value != "" {
			parts = append(parts, "region="+value)
		}
		if value := strings.TrimSpace(meshCfg.PrimaryChannel); value != "" {
			parts = append(parts, "primary_channel="+value)
		}
		if value := strings.TrimSpace(meshCfg.PSKState); value != "" {
			parts = append(parts, "psk_state="+value)
		}
		if len(parts) > 0 {
			hints = append(hints, "Home-node config summary: "+strings.Join(parts, " "))
		}
		if meshCfg.ShareURLAvailable {
			hints = append(hints, "Share-based field-node pairing is available on Progress (Meshtastic share URL).")
		} else {
			hints = append(hints, "Share URL is unavailable; use manual region/channel summary from Progress for field-node setup.")
		}
	} else {
		reason := strings.TrimSpace(meshCfg.UnavailableReason)
		if reason == "" {
			reason = "home node has not reported channel/config details"
		}
		hints = append(hints, "Field-node pairing data is unavailable: "+reason)
		hints = append(hints, "Fallback: open Meshtastic app on the home node and share channel settings manually.")
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
	homeState := strings.TrimSpace(snap.HomeAutoSession.State)
	homeControl := strings.TrimSpace(snap.HomeAutoSession.ControlState)
	if homeState != "" && homeState != "disabled" {
		if source := strings.TrimSpace(snap.HomeAutoSession.EffectiveConfigSource); source != "" {
			hints = append(hints, "Home Auto config source: "+source)
		}
		if ver := strings.TrimSpace(snap.HomeAutoSession.EffectiveConfigVer); ver != "" {
			hints = append(hints, "Home Auto config version: "+ver)
		}
		if result := strings.TrimSpace(snap.HomeAutoSession.LastConfigApplyResult); result != "" {
			hints = append(hints, "Home Auto config apply result: "+result)
		}
		if applyErr := strings.TrimSpace(snap.HomeAutoSession.LastConfigApplyError); applyErr != "" {
			hints = append(hints, "Home Auto config apply error: "+applyErr)
		}
		hints = append(hints, "Home Auto Session state: "+homeState)
		if homeControl != "" {
			hints = append(hints, "Home Auto control state: "+homeControl)
		}
		if reason := strings.TrimSpace(snap.HomeAutoSession.LastDecisionReason); reason != "" {
			hints = append(hints, "Home Auto last decision: "+reason)
		}
		if action := strings.TrimSpace(snap.HomeAutoSession.LastAction); action != "" {
			result := strings.TrimSpace(snap.HomeAutoSession.LastActionResult)
			if result == "" {
				result = "result unavailable"
			}
			hints = append(hints, fmt.Sprintf("Home Auto last action: %s (%s)", action, result))
		}
		if err := strings.TrimSpace(snap.HomeAutoSession.LastError); err != "" {
			hints = append(hints, "Home Auto last error: "+err)
		}
		switch homeState {
		case "misconfigured":
			hints = append(hints, "Home Auto Session is misconfigured. Open Home Auto Session page and verify geofence + tracked node IDs.")
		case "cooldown":
			hints = append(hints, "Home Auto Session is in cooldown after a cloud/session API error. Reevaluate after cooldown window.")
		case "degraded":
			hints = append(hints, "Home Auto Session is degraded. Use Home Auto Session reset action, then reevaluate.")
		case "observe_ready":
			hints = append(hints, "Home Auto Session is in observe mode and will not start/stop cloud sessions.")
		}
		switch strings.TrimSpace(snap.HomeAutoSession.ActiveStateSource) {
		case "local_recovered_unverified":
			hints = append(hints, "Home Auto Session recovered active state from local persisted data; waiting for next confirmed cloud action.")
		case "conflict_unresolved":
			hints = append(hints, "Home Auto Session detected cloud/local control conflict; operator action is required before further control attempts.")
		}
	}
	switch homeControl {
	case "lifecycle_blocked":
		hints = append(hints, "Home Auto Session control is lifecycle-blocked (receiver revoked/disabled/replaced). Reset and re-pair this receiver.")
	case "conflict_blocked":
		hints = append(hints, "Home Auto Session control is conflict-blocked due to cloud/local disagreement. Reevaluate or reset after confirming cloud session state.")
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

func parseHomeAutoSessionConfigForm(r *http.Request) (config.HomeAutoSessionConfig, error) {
	enabled := parseBooleanFormValue(r.Form.Get("enabled"))
	mode := config.HomeAutoSessionMode(strings.ToLower(strings.TrimSpace(r.Form.Get("mode"))))
	if mode == "" {
		mode = config.HomeAutoSessionModeOff
	}
	lat, err := parseFloatField(r.Form.Get("home_lat"), "home latitude")
	if err != nil {
		return config.HomeAutoSessionConfig{}, err
	}
	lon, err := parseFloatField(r.Form.Get("home_lon"), "home longitude")
	if err != nil {
		return config.HomeAutoSessionConfig{}, err
	}
	radius, err := parseFloatField(r.Form.Get("home_radius_m"), "home radius")
	if err != nil {
		return config.HomeAutoSessionConfig{}, err
	}
	startDebounce, err := parseDurationField(r.Form.Get("start_debounce"), "start debounce")
	if err != nil {
		return config.HomeAutoSessionConfig{}, err
	}
	stopDebounce, err := parseDurationField(r.Form.Get("stop_debounce"), "stop debounce")
	if err != nil {
		return config.HomeAutoSessionConfig{}, err
	}
	idleStopTimeout, err := parseDurationField(r.Form.Get("idle_stop_timeout"), "idle stop timeout")
	if err != nil {
		return config.HomeAutoSessionConfig{}, err
	}

	tracked := parseNodeIDsField(r.Form.Get("tracked_node_ids"))
	cloudStart := strings.TrimSpace(r.Form.Get("cloud_start_endpoint"))
	cloudStop := strings.TrimSpace(r.Form.Get("cloud_stop_endpoint"))
	if cloudStart == "" {
		cloudStart = "/api/receiver/home-auto-session/start"
	}
	if cloudStop == "" {
		cloudStop = "/api/receiver/home-auto-session/stop"
	}

	return config.HomeAutoSessionConfig{
		Enabled: enabled,
		Mode:    mode,
		Home: config.HomeGeofenceConfig{
			Lat:     lat,
			Lon:     lon,
			RadiusM: radius,
		},
		TrackedNodeIDs:       tracked,
		StartDebounce:        config.Duration(startDebounce),
		StopDebounce:         config.Duration(stopDebounce),
		IdleStopTimeout:      config.Duration(idleStopTimeout),
		StartupReconcile:     parseBooleanFormValue(r.Form.Get("startup_reconcile")),
		SessionNameTemplate:  strings.TrimSpace(r.Form.Get("session_name_template")),
		SessionNotesTemplate: strings.TrimSpace(r.Form.Get("session_notes_template")),
		Cloud: config.HomeAutoSessionCloudCfg{
			StartEndpoint: cloudStart,
			StopEndpoint:  cloudStop,
		},
	}, nil
}

func parseDurationField(raw string, fieldName string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("%s is required", fieldName)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s", fieldName)
	}
	return parsed, nil
}

func parseFloatField(raw string, fieldName string) (float64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("%s is required", fieldName)
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s", fieldName)
	}
	return parsed, nil
}

func parseNodeIDsField(raw string) []string {
	cleaned := strings.NewReplacer("\r", ",", "\n", ",", ";", ",", "\t", ",").Replace(raw)
	parts := strings.Split(cleaned, ",")
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func parseBooleanFormValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func homeAutoStateHint(snap status.Snapshot) string {
	module := snap.HomeAutoSession
	state := strings.TrimSpace(module.State)
	mode := strings.TrimSpace(module.Mode)
	control := strings.TrimSpace(module.ControlState)

	if control == "lifecycle_blocked" {
		if reason := strings.TrimSpace(module.BlockedReason); reason != "" {
			return "Home Auto Session control is lifecycle-blocked: " + reason
		}
		return "Home Auto Session control is lifecycle-blocked. Reset pairing and link this receiver again."
	}
	if module.DesiredConfigEnabled && (control == "lifecycle_blocked" || control == "conflict_blocked" || state == "degraded") {
		return "Configured policy wants Home Auto Session active, but runtime is blocked/degraded. Resolve blocked reason before expected behavior resumes."
	}
	if control == "conflict_blocked" {
		if reason := strings.TrimSpace(module.BlockedReason); reason != "" {
			return "Home Auto Session is blocked by a cloud/local conflict: " + reason
		}
		return "Home Auto Session is blocked by a cloud/local conflict. Reevaluate or reset after confirming cloud session state."
	}

	switch state {
	case "disabled":
		return "Home Auto Session is disabled. Enable it to use optional geofence-driven session control."
	case "misconfigured":
		return "Home Auto Session config is incomplete or invalid."
	case "observe_ready":
		return homeAutoGPSHint(module, "Waiting for tracked node geofence transition (observe mode).")
	case "control_ready":
		return homeAutoGPSHint(module, "Waiting for tracked node geofence transition (control mode).")
	case "start_pending":
		if module.PendingAction == "start" {
			return "Start action is pending/recovering after restart or retry."
		}
		return "Start candidate detected; waiting for debounce before action."
	case "active":
		if mode == "observe" {
			return "Home Auto Session is tracking an active session in observe mode. Start/stop calls remain disabled in observe mode."
		}
		if action := strings.TrimSpace(module.LastAction); action == "start" && strings.TrimSpace(module.LastActionResult) != "" {
			return "Session active: last start result was " + strings.TrimSpace(module.LastActionResult) + "."
		}
		return "Session active."
	case "stop_pending":
		if module.PendingAction == "stop" {
			return "Stop action is pending/recovering after restart or retry."
		}
		return "Stop candidate detected; waiting for debounce before action."
	case "cooldown":
		if module.PendingAction != "" {
			return "Cloud/session API unavailable; waiting for cooldown before retrying pending action."
		}
		if mode == "observe" {
			return "Observe mode cooldown: waiting before next decision evaluation."
		}
		return "Control mode cooldown: waiting for retry window before another cloud/session action."
	case "degraded":
		if reason := strings.TrimSpace(module.BlockedReason); reason != "" {
			return "Home Auto Session is degraded: " + reason
		}
		return "Home Auto Session is degraded; review last error and reset/reevaluate."
	default:
		if action := strings.TrimSpace(module.LastAction); action != "" {
			result := strings.TrimSpace(module.LastActionResult)
			if result != "" {
				return fmt.Sprintf("Home Auto Session initializing. Last action %s result: %s.", action, result)
			}
		}
		return "Home Auto Session status is initializing."
	}
}

func homeAutoConfigHint(snap status.Snapshot) string {
	module := snap.HomeAutoSession
	source := strings.TrimSpace(module.EffectiveConfigSource)
	version := strings.TrimSpace(module.EffectiveConfigVer)
	applyResult := strings.TrimSpace(module.LastConfigApplyResult)

	switch source {
	case "cloud_managed":
		if !module.DesiredConfigEnabled || strings.TrimSpace(module.DesiredConfigMode) == "off" {
			return "Cloud-managed config is active and currently disables Home Auto Session."
		}
		if version == "" {
			return "Using cloud-managed Home Auto Session config."
		}
		return "Using cloud-managed Home Auto Session config version " + version + "."
	default:
		switch applyResult {
		case "cloud_config_invalid_local_fallback":
			return "Cloud config was invalid and was not applied. Receiver is using local fallback config."
		case "cloud_config_fetch_failed_using_last_effective":
			return "Cloud config fetch failed. Receiver is keeping the last effective local/cloud config safely."
		case "cloud_config_missing_local_fallback":
			return "No cloud-managed config is available. Receiver is using local fallback config."
		case "startup_local_fallback":
			return "Receiver started with local fallback config while awaiting cloud-managed config."
		}
		if version == "" {
			return "Using local fallback Home Auto Session config."
		}
		return "Using local fallback Home Auto Session config version " + version + "."
	}
}

func homeAutoGPSHint(module status.HomeAutoSessionSnapshot, fallback string) string {
	switch strings.TrimSpace(module.GPSStatus) {
	case "missing":
		return "Waiting for tracked node position updates."
	case "invalid":
		return "Ignoring invalid tracked-node GPS coordinates until valid samples arrive."
	case "stale":
		return "Tracked-node GPS sample is stale; waiting for fresh position."
	case "boundary_uncertain":
		return "Tracked node is near geofence boundary; waiting for stable position to avoid flap."
	default:
		return fallback
	}
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
