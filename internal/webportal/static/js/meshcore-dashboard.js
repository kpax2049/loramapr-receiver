(function () {
  "use strict";

  const refreshState = document.getElementById("meshcore-refresh-state");
  const releaseButton = document.getElementById("meshcore-release");
  const resumeButton = document.getElementById("meshcore-resume");
  const normalRefreshInterval = 15000;
  const transitionRefreshInterval = 2000;
  let actionPending = null;
  let latestAdapter = null;
  let refreshTimer = null;
  let refreshInFlight = false;

  function byID(id) { return document.getElementById(id); }
  function text(id, value) { const node = byID(id); if (node) node.textContent = value || "unknown"; }
  function setHidden(node, hidden) {
    if (!node) return;
    node.hidden = hidden;
    if (hidden) node.setAttribute("hidden", "");
    else node.removeAttribute("hidden");
  }
  function actionableError(value) {
    return typeof value === "string" ? value.trim() : "";
  }
  function optional(value, fallback) { return value === undefined || value === null || value === "" ? fallback : String(value); }
  function formatTime(value) {
    if (!value) return "not available";
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "not available" : date.toLocaleString();
  }
  function formatNumber(value, suffix, digits) {
    return value === undefined || value === null ? "unavailable" : Number(value).toFixed(digits || 1) + suffix;
  }
  function setPill(id, label, tone) {
    const node = byID(id);
    if (!node) return;
    node.className = "lmr-pill " + tone;
    node.replaceChildren();
    const dot = document.createElement("span");
    dot.className = "lmr-pill__dot";
    node.append(dot, document.createTextNode(label));
  }
  function toneForState(state) {
    if (state === "Connected") return "is-ok";
    if (state === "Connecting / reconnecting" || state === "Released for external use" || state === "Bluetooth unavailable") return "is-warn";
    if (state === "Error") return "is-fail";
    return "is-neutral";
  }
  function meshcoreState(adapter) {
    if (!adapter) return { label: "Disconnected", detail: "No MeshCore adapter status has been reported." };
    const state = optional(adapter.connection_state, "").toLowerCase();
    const lifecycle = optional(adapter.lifecycle, "").toLowerCase();
    const effectiveState = state || lifecycle || "unknown";
    // A ready connected session is authoritative over stale release flags.
    if (effectiveState === "connected" && adapter.ready) return { label: "Connected", detail: "Companion session handshake is ready." };
    if (effectiveState === "released" || adapter.released_by_user === true) return { label: "Released for external use", detail: "Receiver reconnect is deliberately suppressed until resumed." };
    if (["connecting", "reconnecting", "opening", "handshaking", "detected"].includes(effectiveState) || ["opening", "handshaking", "connecting", "detected"].includes(lifecycle)) return { label: "Connecting / reconnecting", detail: "Receiver is establishing its configured MeshCore connection." };
    if (adapter.transport === "ble" && lifecycle === "not_present") return { label: "Bluetooth unavailable", detail: "The configured BLE device or Bluetooth service is not currently available." };
    if (effectiveState === "error" || ["configuration_error", "incompatible", "degraded", "failed"].includes(lifecycle) || actionableError(adapter.last_error)) return { label: "Error", detail: optional(adapter.last_error, "MeshCore adapter requires attention.") };
    return { label: "Disconnected", detail: "The MeshCore adapter is not connected." };
  }
  function meshcoreAdapter(snapshot) {
    return (snapshot.adapters || []).find(function (adapter) { return String(adapter.protocol || "").toLowerCase() === "meshcore"; });
  }
  function receiverSummary(snapshot) {
    const lifecycle = optional(snapshot.lifecycle, "").toLowerCase();
    const attention = optional(snapshot.attention_state, "").toLowerCase();
    const detail = optional(snapshot.attention_summary, optional(snapshot.failure_summary, "Receiver needs attention"));
    if (lifecycle === "failed") return { label: "Problem", detail: "Receiver service has failed", icon: "✕", tone: "is-fail" };
    if (attention === "urgent") return { label: "Problem", detail: detail, icon: "✕", tone: "is-fail" };
    if (attention === "action_required" || actionableError(snapshot.failure_code)) return { label: "Degraded", detail: detail, icon: "!", tone: "is-warn" };
    if (lifecycle === "stopped" || lifecycle === "stopping" || !lifecycle) return { label: "Offline", detail: "Receiver service is unavailable", icon: "○", tone: "is-neutral" };
    if (lifecycle === "running" && snapshot.ready) return { label: "Online", detail: "Receiver service is ready", icon: "✓", tone: "is-ok" };
    return { label: "Degraded", detail: "Receiver service is not ready", icon: "!", tone: "is-warn" };
  }
  function cloudSummary(snapshot) {
    const state = optional(snapshot.cloud_status, "").toLowerCase();
    if (snapshot.cloud_reachable === true || state === "reachable") return { label: "Cloud connected", detail: "Cloud is reachable", icon: "✓", tone: "is-ok" };
    if (state === "unreachable" || state === "lifecycle_blocked") return { label: "Cloud unreachable", detail: "Cloud connection needs attention", icon: "!", tone: "is-warn" };
    return { label: "Cloud unavailable", detail: "Cloud status is not available", icon: "○", tone: "is-neutral" };
  }
  function meshcoreSummary(adapter) {
    const view = meshcoreState(adapter);
    if (view.label === "Connected") return { label: "MeshCore connected", detail: "Companion handshake ready", icon: "✓", tone: "is-ok" };
    if (view.label === "Connecting / reconnecting") return { label: "MeshCore reconnecting", detail: "Restoring receiver connection", icon: "!", tone: "is-warn" };
    if (view.label === "Released for external use") return { label: "MeshCore released", detail: "Available to another client", icon: "○", tone: "is-neutral" };
    if (view.label === "Error") return { label: "MeshCore error", detail: view.detail, icon: "✕", tone: "is-fail" };
    return { label: "MeshCore unavailable", detail: view.detail, icon: "○", tone: "is-neutral" };
  }
  function setSummary(containerID, iconID, labelID, detailID, summary) {
    const container = byID(containerID);
    if (container) container.className = container.className.replace(/\bis-(ok|warn|fail|neutral)\b/g, "").trim() + " " + summary.tone;
    text(iconID, summary.icon);
    text(labelID, summary.label);
    text(detailID, summary.detail);
  }
  function renderReceiver(snapshot) {
    const receiver = receiverSummary(snapshot);
    setSummary("receiver-overall", "receiver-overall-icon", "receiver-overall-label", "receiver-overall-detail", receiver);
    setSummary("cloud-summary", "cloud-summary-icon", "cloud-summary-label", "cloud-summary-detail", cloudSummary(snapshot));
    text("receiver-lifecycle", optional(snapshot.lifecycle, "unknown"));
    text("receiver-cloud", optional(snapshot.cloud_status, "unknown"));
    const started = snapshot.started_at ? new Date(snapshot.started_at) : null;
    text("receiver-uptime", started && !Number.isNaN(started.getTime()) ? formatDuration(Date.now() - started.getTime()) : "unavailable");
  }
  function formatDuration(milliseconds) {
    const seconds = Math.max(0, Math.floor(milliseconds / 1000));
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    return (days ? days + "d " : "") + hours + "h " + minutes + "m";
  }
  function transitionText(action) {
    return action === "release" ? "Releasing device…" : "Reconnecting to MeshCore…";
  }
  function setTransition(action) {
    const transition = byID("meshcore-transition");
    if (!action) {
      setHidden(transition, true);
      return;
    }
    text("meshcore-transition-text", transitionText(action));
    setHidden(transition, false);
  }
  function setLifecycleButtons(action) {
    const pending = actionPending && actionPending.action;
    if (!pending) {
      releaseButton.disabled = false;
      resumeButton.disabled = false;
      releaseButton.textContent = "Release device";
      resumeButton.textContent = "Resume receiver connection";
      return;
    }
    releaseButton.disabled = true;
    resumeButton.disabled = true;
    releaseButton.textContent = pending === "release" ? "Releasing…" : "Release device";
    resumeButton.textContent = pending === "resume" ? "Resuming…" : "Resume receiver connection";
  }
  function pendingSettled(view) {
    if (!actionPending || !actionPending.apiSucceeded) return false;
    return actionPending.action === "release" ? view.label === "Released for external use" : view.label === "Connected";
  }
  function renderPendingAdapter(adapter, view) {
    const releasing = actionPending.action === "release";
    const label = releasing ? "Releasing device…" : "Connecting / reconnecting";
    const detail = releasing ? "Disconnecting the receiver from this MeshCore device." : "Receiver is attempting to reconnect; waiting for the Companion handshake.";
    setPill("meshcore-state", label, "is-warn");
    text("meshcore-state-detail", detail);
    setSummary("meshcore-summary", "meshcore-summary-icon", "meshcore-summary-label", "meshcore-summary-detail", {
      label: releasing ? "MeshCore releasing" : "MeshCore reconnecting", detail: detail, icon: "!", tone: "is-warn"
    });
    text("meshcore-configured", optional(adapter && adapter.configured_device, "not configured"));
    text("meshcore-connected", optional(adapter && adapter.connected_device, "not connected"));
    text("meshcore-reconnect", releasing ? "release in progress" : "reconnecting");
    text("meshcore-ready", "not ready");
    setHidden(byID("meshcore-release-note"), true);
    setHidden(releaseButton, !releasing);
    setHidden(resumeButton, releasing);
    setLifecycleButtons(actionPending.action);
    setTransition(actionPending.action);
  }
  function finishTransition() {
    actionPending = null;
    setTransition(null);
    scheduleRefresh(normalRefreshInterval);
  }
  function renderAdapter(adapter) {
    const view = meshcoreState(adapter);
    latestAdapter = adapter;
    updateError(actionableError(adapter && adapter.last_error), "Adapter error: ");
    if (actionPending) {
      if (pendingSettled(view)) {
        finishTransition();
      } else {
        renderPendingAdapter(adapter, view);
        return;
      }
    }
    setSummary("meshcore-summary", "meshcore-summary-icon", "meshcore-summary-label", "meshcore-summary-detail", meshcoreSummary(adapter));
    setPill("meshcore-state", view.label, toneForState(view.label));
    text("meshcore-state-detail", view.detail);
    text("meshcore-configured", optional(adapter && adapter.configured_device, "not configured"));
    text("meshcore-connected", optional(adapter && adapter.connected_device, "not connected"));
    const released = view.label === "Released for external use";
    text("meshcore-reconnect", released ? "suppressed by release" : "enabled");
    text("meshcore-ready", adapter && adapter.ready ? "ready" : "not ready");
    setHidden(byID("meshcore-release-note"), !released);
    setHidden(releaseButton, !(adapter && view.label === "Connected"));
    setHidden(resumeButton, !released);
    setLifecycleButtons(null);
    setTransition(null);
  }
  function errorPanel() {
    let panel = byID("meshcore-error");
    if (panel) return panel;
    panel = document.createElement("div");
    panel.id = "meshcore-error";
    panel.className = "lmr-callout is-fail";
    panel.hidden = true;
    const icon = document.createElement("span");
    icon.className = "lmr-callout__icon";
    icon.textContent = "x";
    panel.append(icon, document.createElement("div"));
    const note = byID("meshcore-release-note");
    note.parentNode.insertBefore(panel, note);
    return panel;
  }
  function updateError(message, prefix) {
    const textValue = actionableError(message);
    const existing = byID("meshcore-error");
    if (!textValue) {
      if (existing) {
        existing.lastElementChild.textContent = "";
        setHidden(existing, true);
      }
      return;
    }
    const panel = existing || errorPanel();
    panel.lastElementChild.textContent = (prefix || "") + textValue;
    setHidden(panel, false);
  }
  function renderTracking(tracking) {
    if (!tracking) {
      setPill("tracking-active", "Unavailable", "is-neutral");
      return;
    }
    setPill("tracking-active", tracking.active ? "Active" : "Inactive", tracking.active ? "is-ok" : "is-neutral");
    text("tracking-target", optional(tracking.targetPublicKey, "none"));
    text("tracking-motion", optional(tracking.motionState, "unknown"));
    text("tracking-speed", formatNumber(tracking.estimatedSpeedKmh, " km/h", 1));
    text("tracking-interval", optional(tracking.currentIntervalSeconds, "unknown") + " s");
    text("tracking-failures", optional(tracking.consecutiveFailures, "0"));
    text("tracking-next", tracking.nextRequestAt ? formatTime(tracking.nextRequestAt) : "not scheduled");
    text("tracking-recovery", optional(tracking.routeRecoveryState, "idle"));
    text("tracking-recovery-event", optional(tracking.lastRouteRecoveryEvent, "none"));
    renderTelemetry(tracking.latestTelemetry);
    renderPolls(tracking.recentPolls || []);
  }
  function renderTelemetry(latest) {
    if (!latest) {
      setPill("telemetry-status", "No observation", "is-neutral");
      ["telemetry-location", "telemetry-altitude", "telemetry-voltage", "telemetry-temperature", "telemetry-received", "telemetry-correlation"].forEach(function (id) { text(id, "unavailable"); });
      return;
    }
    setPill("telemetry-status", "Observed", "is-ok");
    const telemetry = latest.telemetry || {};
    const location = telemetry.latitude !== undefined && telemetry.longitude !== undefined ? Number(telemetry.latitude).toFixed(6) + ", " + Number(telemetry.longitude).toFixed(6) : "unavailable";
    text("telemetry-location", location);
    text("telemetry-altitude", formatNumber(telemetry.altitudeM, " m", 1));
    text("telemetry-voltage", formatNumber(telemetry.voltage, " V", 2));
    text("telemetry-temperature", formatNumber(telemetry.temperatureC, " °C", 1));
    text("telemetry-received", formatTime(latest.receivedAt));
    text("telemetry-correlation", latest.sourcePrefix ? "prefix matched: " + latest.sourcePrefix : "request correlated");
  }
  function routeLabel(route) {
    const mode = route && route.mode;
    if (mode === "zero_hop") return "Direct / 0-hop";
    if (mode === "flood") return "Flood";
    if (mode === "explicit_path") return "Explicit path";
    return "Unknown";
  }
  function addCell(row, main, sub) {
    const cell = document.createElement("td");
    cell.textContent = main;
    if (sub) { const detail = document.createElement("div"); detail.className = "lmr-sub"; detail.textContent = sub; cell.appendChild(detail); }
    row.appendChild(cell);
  }
  function renderPolls(polls) {
    const body = byID("tracking-polls");
    if (!body) return;
    body.replaceChildren();
    if (!polls.length) { const row = document.createElement("tr"); const cell = document.createElement("td"); cell.colSpan = 6; cell.className = "lmr-cell-soft"; cell.textContent = "No telemetry polls recorded."; row.appendChild(cell); body.appendChild(row); return; }
    polls.slice().reverse().forEach(function (poll) {
      const row = document.createElement("tr");
      addCell(row, formatTime(poll.requestAt), optional(poll.outcome, "unknown") + (poll.error ? ": " + poll.error : ""));
      const route = poll.routeAttempt || {};
      const path = route.path && route.path.length ? "Raw path hashes: " + route.path.join(", ") : "Path length: " + optional(route.pathLength, "unknown");
      addCell(row, routeLabel(route), path + "; response route unknown");
      addCell(row, optional(poll.motionState, "unknown"), formatNumber(poll.estimatedSpeedKmh, " km/h", 1));
      addCell(row, optional(poll.intervalSeconds, "unknown") + " s");
      addCell(row, optional(poll.routeRecovery, "none"), poll.responseRouteUnknown ? "Response route unknown" : "Response route supplied");
      addCell(row, poll.pathUpdateObserved ? "Path update observed" : "No path update", "Failures: " + optional(poll.consecutiveFailures, "0"));
      body.appendChild(row);
    });
  }
  async function json(url, options) {
    const response = await fetch(url, options);
    let payload = null;
    try { payload = await response.json(); } catch (_) { /* handled below */ }
    if (!response.ok) throw new Error((payload && payload.error) || (payload && payload.outcome) || "Request failed (" + response.status + ")");
    return payload;
  }
  async function refresh() {
    if (refreshInFlight) return;
    refreshInFlight = true;
    try {
      const results = await Promise.all([json("/api/status"), json("/api/meshcore/tracking/status").catch(function () { return null; })]);
      renderReceiver(results[0]);
      renderAdapter(meshcoreAdapter(results[0]));
      renderTracking(results[1]);
      refreshState.textContent = actionPending ? transitionText(actionPending.action) : "Updated " + new Date().toLocaleTimeString();
    } catch (error) {
      refreshState.textContent = "Status refresh failed: " + error.message;
    } finally {
      refreshInFlight = false;
    }
  }
  function scheduleRefresh(delay) {
    if (refreshTimer) window.clearTimeout(refreshTimer);
    refreshTimer = window.setTimeout(function () {
      refresh().finally(function () { scheduleRefresh(actionPending ? transitionRefreshInterval : normalRefreshInterval); });
    }, delay);
  }
  async function lifecycle(action) {
    if (actionPending) return;
    actionPending = { action: action, apiSucceeded: false };
    renderPendingAdapter(latestAdapter, meshcoreState(latestAdapter));
    updateError("");
    refreshState.textContent = transitionText(action);
    scheduleRefresh(250);
    try {
      await json("/api/meshcore/adapter/" + action, { method: "POST" });
      actionPending.apiSucceeded = true;
      refreshState.textContent = action === "release" ? "Release accepted; waiting for receiver status…" : "Reconnect requested; waiting for Companion handshake…";
      await refresh();
    } catch (error) {
      finishTransition();
      renderAdapter(latestAdapter);
      updateError(error && error.message, action === "release" ? "Release failed: " : "Resume failed: ");
      refreshState.textContent = "Action failed";
    }
  }
  if (releaseButton) releaseButton.addEventListener("click", function () { lifecycle("release"); });
  if (resumeButton) resumeButton.addEventListener("click", function () { lifecycle("resume"); });
  refresh();
  scheduleRefresh(normalRefreshInterval);
}());
