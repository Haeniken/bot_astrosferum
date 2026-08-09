import { APIError, AstrosferumAPI } from "./api.js";
import { ContractError } from "./contract.js";
import { buildDomeGeometry } from "./geometry.js";
import { createTranslator, formatDuration, formatInstant, normalizeLocale } from "./i18n.js";
import { LAYERS, legendForLayer } from "./palette.js";
import { CanvasDomeRenderer } from "./renderer-canvas.js";
import { WebGLDomeRenderer } from "./renderer-webgl.js";
import { ViewController } from "./view-controller.js";

const api = new AstrosferumAPI();
const elements = collectElements();
const AVAILABILITY_POLL_INTERVAL_MS = 10_000;
const NIGHT_MODE_STORAGE_KEY = "astrosferum.red-night-mode";

const locale = localeFromPath(window.location.pathname);
const translate = createTranslator(locale);
let profile = null;
let availability = null;
let points = [];
let visualizations = [];
let currentJob = null;
let jobController = null;
let datasetView = null;
let domeGeometry = null;
let webglRenderer = null;
let canvasRenderer = null;
let viewController = null;
let resizeObserver = null;
let resizeFallback = null;
let displayMode = "webgl";
let viewerReady = false;
let frameIndex = 0;
let selectedNode = -1;
let availabilityPollTimer = null;
let availabilityPollController = null;
let availabilityRequestInFlight = false;
let redNightMode = restoreNightModePreference();
let fallbackViewerExpanded = false;
let fallbackViewerReturnFocus = null;

bindEvents();
setNightMode(redNightMode, false);
applyLanguage();
void bootstrap();

async function bootstrap() {
  const controller = new AbortController();
  try {
    profile = await api.profile(controller.signal);
    if (profile.authenticated) {
      if (profile.language !== locale) {
        window.location.replace(localizedShellPath(profile.language));
        return;
      }
      showAuthenticatedProfile();
      points = await api.points(controller.signal);
      try {
        visualizations = await api.visualizations(controller.signal);
      } catch {
        // A catalogue outage must not hide saved points or model availability.
        visualizations = [];
      }
      populatePoints();
      renderVisualizations();
      await refreshAvailability();
      scheduleAvailabilityPoll();
    } else {
      try {
        visualizations = await api.visualizations(controller.signal);
      } catch {
        visualizations = [];
      }
      renderVisualizations();
      showSignedOut();
    }
  } catch (error) {
    document.documentElement.dataset.sessionState = "error";
    showFatal(error, false);
  }
}

function bindEvents() {
  elements.pointMode.forEach((input) => input.addEventListener("change", updatePointMode));
  elements.pointForm.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitJob();
  });
  elements.logoutButton.addEventListener("click", () => void logout());
  elements.languageButton.addEventListener("click", (event) => {
    if (!profile?.authenticated) {
      return;
    }
    event.preventDefault();
    void persistLanguageAndNavigate(elements.languageButton.hreflang, elements.languageButton.href);
  });
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && profile?.authenticated) {
      scheduleAvailabilityPoll(0);
    }
  });
  elements.cancelJobButton.addEventListener("click", () => void cancelJob());
  elements.savedVisualizationsList.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-visualization-id]");
    if (button) {
      void openSavedVisualization(button.dataset.visualizationId);
    }
  });
  elements.layerSelect.addEventListener("change", () => {
    if (!LAYERS.includes(elements.layerSelect.value)) {
      return;
    }
    updateLegend();
    updateFrame();
  });
  elements.timeSlider.addEventListener("input", () => {
    frameIndex = Number(elements.timeSlider.value);
    updateFrame();
  });
  elements.webglModeButton.addEventListener("click", () => switchMode("webgl"));
  elements.canvasModeButton.addEventListener("click", () => switchMode("canvas"));
  elements.stageModeButton.addEventListener("click", () => {
    switchMode(displayMode === "webgl" ? "canvas" : "webgl");
  });
  elements.fullscreenButton.addEventListener("click", () => void toggleViewerExpansion());
  elements.nightModeButton.addEventListener("click", () => setNightMode(!redNightMode));
  document.addEventListener("fullscreenchange", syncViewerExpansion);
  document.addEventListener("webkitfullscreenchange", syncViewerExpansion);
  document.addEventListener("keydown", handleViewerExpansionKeydown);
  elements.mapCanvas.addEventListener("click", (event) => {
    if (!canvasRenderer) {
      return;
    }
    selectNode(canvasRenderer.pick(event.clientX, event.clientY), true);
  });
  elements.mapCanvas.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && domeGeometry) {
      event.preventDefault();
      selectNode(domeGeometry.profile.nodeCount - 1, true);
    }
  });
  elements.dataTableDetails.addEventListener("toggle", () => {
    if (elements.dataTableDetails.open) {
      renderDataTable();
    }
  });
  elements.dataTableBody.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-node-index]");
    if (!button) {
      return;
    }
    selectNode(Number(button.dataset.nodeIndex), false);
    elements.inspectTitle.scrollIntoView({ behavior: "auto", block: "start" });
  });
}

async function persistLanguageAndNavigate(language, target) {
  const controller = new AbortController();
  try {
    await api.setLanguage(normalizeLocale(language), controller.signal);
  } catch {
    // Language switching remains available during a transient preferences outage.
  }
  window.location.assign(target);
}

function applyLanguage() {
  document.documentElement.lang = locale;
  document.title = translate("app.title");
  elements.metaDescription.setAttribute("content", translate("app.description"));
  for (const node of document.querySelectorAll("[data-i18n]")) {
    node.textContent = translate(node.dataset.i18n);
  }
  const currentPath = localizedShellPath(locale);
  const alternateLocale = locale === "ru" ? "en" : "ru";
  elements.brandLink.href = currentPath;
  elements.languageButton.href = localizedShellPath(alternateLocale);
  elements.languageButton.hreflang = alternateLocale;
  elements.languageButton.textContent = alternateLocale.toUpperCase();
  elements.languageButton.setAttribute("aria-label", translate(`language.switchTo.${alternateLocale}`));
  elements.languageButton.title = translate(`language.switchTo.${alternateLocale}`);
  elements.loginButton.href = `/auth/telegram/login?lang=${encodeURIComponent(locale)}`;
  updateNightModeControl();
  updateViewControls();
  updateFullscreenControl();
  if (profile?.authenticated) {
    elements.profileGreeting.textContent = translate("profile.greeting", { name: profile.displayName });
  }
  if (availability) {
    showAvailability();
  }
  if (datasetView) {
    updateLegend();
    updateFrame();
  }
  if (visualizations.length > 0) {
    renderVisualizations();
  }
  document.documentElement.dataset.languageReady = "true";
}

function showAuthenticatedProfile() {
  elements.profileGreeting.hidden = false;
  elements.profileGreeting.textContent = translate("profile.greeting", { name: profile.displayName });
  elements.loginButton.hidden = true;
  elements.logoutButton.hidden = false;
  elements.authenticationNotice.hidden = true;
  elements.pointForm.hidden = false;
  elements.savedVisualizationsPanel.hidden = false;
  document.documentElement.dataset.sessionState = "authenticated";
}

function showSignedOut() {
  stopAvailabilityPolling();
  elements.profileGreeting.hidden = true;
  elements.loginButton.hidden = false;
  elements.logoutButton.hidden = true;
  elements.authenticationNotice.hidden = false;
  elements.pointForm.hidden = true;
  elements.savedVisualizationsPanel.hidden = visualizations.length === 0;
  elements.availabilityText.textContent = translate("auth.required");
  elements.availabilitySignal.dataset.state = "loading";
  document.documentElement.dataset.sessionState = "anonymous";
}

function localeFromPath(pathname) {
  const match = /^\/(ru|en)(?:\/|$)/.exec(pathname);
  if (match) {
    return match[1];
  }
  return normalizeLocale(navigator.language || "en");
}

function localizedShellPath(language) {
  return `/${normalizeLocale(language)}/account/sky-conditions`;
}

function populatePoints() {
  elements.pointSelect.replaceChildren();
  for (const point of points) {
    const option = document.createElement("option");
    option.value = point.id;
    option.textContent = `${point.name} \u00b7 ${formatCoordinate(point.latitude)}, ${formatCoordinate(point.longitude)}`;
    elements.pointSelect.append(option);
  }
  if (points.length === 0) {
    elements.pointMode.find((input) => input.value === "manual").checked = true;
  }
  updatePointMode();
}

function updatePointMode() {
  const selected = elements.pointMode.find((input) => input.checked)?.value ?? "saved";
  const useSaved = selected === "saved" && points.length > 0;
  elements.savedPointFields.hidden = !useSaved;
  elements.manualPointFields.hidden = useSaved;
  elements.pointSelect.disabled = !useSaved;
  elements.latitudeInput.disabled = useSaved;
  elements.longitudeInput.disabled = useSaved;
  elements.pointError.hidden = true;
}

function showAvailability() {
  const usable = availability.enabled && availability.available && availability.workerAvailable &&
    availability.provider === "icon-eu" && availability.gridProfile;
  let state = "ready";
  let message = translate("availability.ready");
  if (!availability.enabled) {
    state = "error";
    message = translate("availability.disabled");
  } else if (!availability.workerAvailable) {
    state = "error";
    message = translate("availability.workerDown");
  } else if (!availability.available) {
    state = availability.reason === "storage_profile_unavailable" ? "error" : "warning";
    message = translate(availabilityReasonMessage(availability.reason));
  } else if (availability.stale) {
    state = "warning";
    message = translate("availability.stale");
  }
  elements.availabilitySignal.dataset.state = state;
  elements.availabilityText.textContent = message;
  elements.availabilityRun.textContent = availability.runID === null ? "\u2014" : formatRunID(availability.runID);
  elements.availabilityFreshness.textContent = availability.freshnessSeconds === null
    ? "\u2014" : formatDuration(availability.freshnessSeconds, locale);
  const workerStatus = availability.workerAvailable
    ? translate(availability.running ? "availability.running" : "availability.idle")
    : translate("availability.workerDown");
  elements.availabilityDetails.textContent = `${translate("availability.queue", { count: availability.queueDepth })} \u00b7 ${workerStatus}`;
  const jobActive = currentJob && ["queued", "running"].includes(currentJob.state);
  elements.calculateButton.disabled = !usable || jobActive;
  elements.runChip.querySelector("b").textContent = availability.runID === null ? "\u2014" : formatRunID(availability.runID);
  elements.freshnessChip.querySelector("b").textContent = availability.freshnessSeconds === null
    ? "\u2014" : formatDuration(availability.freshnessSeconds, locale);
}

function availabilityReasonMessage(reason) {
  switch (reason) {
  case "dome_run_unavailable":
    return "availability.modelPreparing";
  case "storage_profile_unavailable":
    return "availability.profileUnavailable";
  case "forecast_window_unavailable":
    return "availability.windowUnavailable";
  default:
    return "availability.modelUnavailable";
  }
}

async function refreshAvailability() {
  if (!profile?.authenticated || availabilityRequestInFlight) {
    return;
  }
  availabilityRequestInFlight = true;
  const controller = new AbortController();
  availabilityPollController = controller;
  try {
    availability = await api.availability(controller.signal);
    showAvailability();
  } catch (error) {
    if (error.name !== "AbortError") {
      showAvailabilityFailure();
    }
  } finally {
    if (availabilityPollController === controller) {
      availabilityPollController = null;
    }
    availabilityRequestInFlight = false;
  }
}

function showAvailabilityFailure() {
  elements.availabilitySignal.dataset.state = "error";
  elements.availabilityText.textContent = translate("availability.statusUnavailable");
  elements.availabilityDetails.textContent = translate("availability.statusUnavailable");
  elements.calculateButton.disabled = true;
}

function scheduleAvailabilityPoll(delayMilliseconds = AVAILABILITY_POLL_INTERVAL_MS) {
  if (!profile?.authenticated) {
    return;
  }
  if (availabilityPollTimer !== null) {
    window.clearTimeout(availabilityPollTimer);
  }
  availabilityPollTimer = window.setTimeout(async () => {
    availabilityPollTimer = null;
    await refreshAvailability();
    scheduleAvailabilityPoll();
  }, delayMilliseconds);
}

function stopAvailabilityPolling() {
  if (availabilityPollTimer !== null) {
    window.clearTimeout(availabilityPollTimer);
    availabilityPollTimer = null;
  }
  availabilityPollController?.abort();
  availabilityPollController = null;
}

async function submitJob() {
  elements.pointError.hidden = true;
  let request;
  const mode = elements.pointMode.find((input) => input.checked)?.value ?? "saved";
  if (mode === "saved" && points.length > 0) {
    if (!elements.pointSelect.value) {
      showPointError(translate("point.invalid"));
      return;
    }
    request = { point_id: Number(elements.pointSelect.value) };
  } else {
    const latitude = Number(elements.latitudeInput.value);
    const longitude = Number(elements.longitudeInput.value);
    if (!Number.isFinite(latitude) || latitude < -90 || latitude > 90 ||
        !Number.isFinite(longitude) || longitude < -180 || longitude > 180) {
      showPointError(translate("point.invalid"));
      return;
    }
    request = { coordinates: { latitude, longitude } };
  }
  request.idempotency_key = idempotencyKey();
  stopJobPolling();
  jobController = new AbortController();
  setJobStatus("running", locale === "ru" ? "Ставлю расчёт в очередь\u2026" : "Submitting the calculation\u2026");
  elements.calculateButton.disabled = true;
  try {
    currentJob = await api.createJob(request, jobController.signal);
    updateJobStatus(currentJob);
    await pollJob(currentJob, jobController.signal);
  } catch (error) {
    if (error.name !== "AbortError") {
      setJobStatus("failed", userError(error));
    }
  } finally {
    if (availability) {
      showAvailability();
    }
  }
}

async function pollJob(initial, signal) {
  let job = initial;
  let polls = 0;
  while (!signal.aborted) {
    if (job.state === "ready") {
      setJobStatus("loading", translate("job.loading"));
      const verified = await api.result(job.id, signal);
      if (verified.dataset.run_id !== job.runID ||
          verified.profile.name !== job.gridProfile ||
          verified.profile.digest !== job.gridGeometryDigest) {
        throw new ContractError("$", "job identity and result dataset do not match");
      }
      await presentDataset(verified);
      try {
        await refreshVisualizations(signal);
      } catch {
        // The calculated dataset remains usable if catalogue refresh fails.
      }
      setJobStatus("ready", translate("job.ready"));
      return;
    }
    if (["failed", "cancelled"].includes(job.state)) {
      updateJobStatus(job);
      return;
    }
    await delay(polls < 10 ? 2000 : 5000, signal);
    job = await api.job(job.id, signal);
    currentJob = job;
    updateJobStatus(job);
    polls += 1;
  }
}

async function refreshVisualizations(signal) {
  visualizations = await api.visualizations(signal);
  renderVisualizations();
}

function renderVisualizations() {
  elements.savedVisualizationsList.replaceChildren();
  elements.savedVisualizationsEmpty.hidden = visualizations.length !== 0;
  for (const visualization of visualizations) {
    const item = document.createElement("li");
    const button = document.createElement("button");
    button.type = "button";
    button.dataset.visualizationId = visualization.id;
    const title = document.createElement("strong");
    title.textContent = visualization.name || `${formatCoordinate(visualization.latitude)}, ${formatCoordinate(visualization.longitude)}`;
    const metadata = document.createElement("span");
    metadata.textContent = `${formatRunID(visualization.run_id)} · ${visualization.admin_fixture
      ? translate("saved.permanent")
      : translate("saved.remaining", { remaining: formatRemaining(visualization.expires_at) })}`;
    button.append(title, metadata);
    item.append(button);
    elements.savedVisualizationsList.append(item);
  }
}

async function openSavedVisualization(id) {
  const controller = new AbortController();
  setJobStatus("loading", translate("saved.loading"));
  try {
    const metadata = visualizations.find((visualization) => visualization.id === id);
    if (!metadata) {
      throw new ContractError("$.visualizations", "selected visualization is absent from the validated catalogue");
    }
    const verified = await api.savedResult(id, controller.signal);
    await presentDataset(verified);
    setJobStatus("ready", translate("saved.opened"));
  } catch (error) {
    if (error instanceof APIError && error.status === 404) {
      try {
        await refreshVisualizations(controller.signal);
      } catch {
        // Preserve the original expiry/not-found error.
      }
    }
    setJobStatus("failed", userError(error));
  }
}

function updateJobStatus(job) {
  switch (job.state) {
  case "queued":
    setJobStatus("queued", translate("job.queued", { position: job.queuePosition ?? "\u2014" }));
    elements.cancelJobButton.hidden = false;
    break;
  case "running":
    setJobStatus("running", translate("job.running"));
    elements.cancelJobButton.hidden = false;
    break;
  case "ready":
    setJobStatus("ready", translate("job.ready"));
    elements.cancelJobButton.hidden = true;
    break;
  case "failed":
    setJobStatus("failed", translate("job.failed", { code: job.failureCode ?? "unknown" }));
    elements.cancelJobButton.hidden = true;
    break;
  case "cancelled":
    setJobStatus("failed", translate("job.cancelled"));
    elements.cancelJobButton.hidden = true;
    break;
  }
}

function setJobStatus(state, message) {
  elements.jobProgress.dataset.state = state;
  elements.jobStatus.textContent = message;
}

async function cancelJob() {
  if (!currentJob || !["queued", "running"].includes(currentJob.state)) {
    return;
  }
  try {
    await api.cancelJob(currentJob.id, jobController?.signal);
    stopJobPolling();
    setJobStatus("failed", translate("job.cancelled"));
    elements.cancelJobButton.hidden = true;
  } catch (error) {
    setJobStatus("failed", userError(error));
  }
}

async function logout() {
  stopAvailabilityPolling();
  const controller = new AbortController();
  try {
    await api.logout(controller.signal);
    window.location.assign("/");
  } catch (error) {
    showFatal(error, false);
  }
}

async function presentDataset(verified) {
  destroyRenderers();
  datasetView = verified;
  frameIndex = 0;
  selectedNode = verified.profile.nodeCount - 1;
  elements.emptyState.hidden = true;
  elements.fatalError.hidden = true;
  elements.domeStage.hidden = false;
  elements.dataTableDetails.hidden = false;
  elements.timeSlider.disabled = false;
  elements.layerSelect.disabled = false;
  elements.timeSlider.value = "0";
  elements.timeSlider.max = String(verified.dataset.frames.length - 1);
  updateViewControls();
  updateFullscreenControl();
  await nextPaint();
  domeGeometry = buildDomeGeometry(verified.profile);
  canvasRenderer = new CanvasDomeRenderer(elements.mapCanvas, domeGeometry, translate);
  try {
    webglRenderer = new WebGLDomeRenderer(elements.domeCanvas, elements.overlayCanvas, domeGeometry, translate);
    viewController = new ViewController(
      elements.domeCanvas,
      (state) => webglRenderer?.render(state),
      (direction) => selectNode(webglRenderer?.pick(direction) ?? -1, true),
    );
    elements.webglNotice.textContent = translate("controls.help");
  } catch {
    webglRenderer = null;
    displayMode = "canvas";
    elements.webglNotice.textContent = translate("view.webglUnavailable");
  }
  observeResize();
  updateLegend();
  viewerReady = true;
  switchMode(displayMode);
  updateFrame();
  updateFullscreenControl();
}

function updateFrame() {
  if (!datasetView || !domeGeometry) {
    return;
  }
  const frame = datasetView.dataset.frames[frameIndex];
  const layer = LAYERS.includes(elements.layerSelect.value) ? elements.layerSelect.value : "overall";
  document.body.dataset.twilight = frame.twilight_band;
  elements.twilightChip.textContent = translate(`twilight.${frame.twilight_band}`);
  elements.runChip.querySelector("b").textContent = formatRunID(datasetView.dataset.run_id);
  const validAt = datasetView.dataset.valid_times[frameIndex];
  const timeZone = datasetView.dataset.requested_location.time_zone;
  elements.localTime.textContent = `${translate("time.local")}: ${formatInstant(validAt, locale, timeZone)}`;
  elements.utcTime.textContent = `${translate("time.utc")}: ${formatInstant(validAt, locale, "UTC")}`;
  elements.timePosition.textContent = translate("time.position", {
    current: frameIndex + 1,
    total: datasetView.dataset.frames.length,
  });
  elements.timeSlider.setAttribute("aria-valuetext", `${elements.localTime.textContent}; ${translate(`twilight.${frame.twilight_band}`)}`);
  webglRenderer?.setFrame(frame, layer);
  canvasRenderer?.setFrame(frame, layer);
  webglRenderer?.setSelected(selectedNode);
  canvasRenderer?.setSelected(selectedNode);
  renderActive();
  renderInspector();
  if (!elements.domeTooltip.hidden) {
    renderTooltip();
  }
  if (elements.dataTableDetails.open) {
    renderDataTable();
  } else {
    elements.dataTableBody.replaceChildren();
  }
}

function selectNode(index, showTooltip) {
  if (!datasetView || !Number.isInteger(index) || index < 0 || index >= datasetView.profile.nodeCount) {
    clearTooltip();
    return;
  }
  selectedNode = index;
  webglRenderer?.setSelected(index);
  canvasRenderer?.setSelected(index);
  renderActive();
  renderInspector();
  if (showTooltip) {
    renderTooltip();
  }
}

function renderInspector() {
  if (!datasetView || selectedNode < 0) {
    elements.inspectEmpty.hidden = false;
    elements.inspectContent.hidden = true;
    return;
  }
  const node = datasetView.dataset.frames[frameIndex].nodes[selectedNode];
  elements.inspectEmpty.hidden = true;
  elements.inspectContent.hidden = false;
  elements.inspectOverall.textContent = node.overall === null ? "\u2014" : node.overall.toFixed(1);
  elements.inspectAzimuth.textContent = node.azimuth_deg === null ? translate("inspect.zenith") : `${node.azimuth_deg.toFixed(1)}\u00b0`;
  elements.inspectElevation.textContent = `${node.elevation_deg.toFixed(node.elevation_deg % 1 ? 3 : 0)}\u00b0`;
  elements.inspectState.textContent = translate(`state.${node.state}`);
  elements.inspectQuality.textContent = translate(`quality.${node.data_quality}`);
  elements.inspectSeeing.textContent = metric(node.seeing_arcsec_500nm, "\u2033", 2);
  const tau = node.tau0_ms_500nm ?? node.tau0_conservative_ms_500nm;
  elements.inspectTau.textContent = metric(tau, " ms", 2);
  elements.inspectCloud.textContent = node.effective_cloud_transmission === null
    ? translate("inspect.notAvailable") : `${((1 - node.effective_cloud_transmission) * 100).toFixed(1)}%`;
  elements.inspectPWV.textContent = metric(node.slant_water_vapour_kg_m2, " kg/m\u00b2", 2);
  elements.inspectFactor.textContent = translate(`factor.${node.limiting_factor}`);
  renderPenalties(node);
  renderQuality(node);
}

function renderPenalties(node) {
  elements.penaltyList.replaceChildren();
  for (const contribution of node.penalty_contributions) {
    const row = document.createElement("div");
    row.className = "penalty-row";
    const label = document.createElement("div");
    label.className = "penalty-label";
    const name = document.createElement("span");
    name.textContent = translate(`penalty.${contribution.key}`);
    const value = document.createElement("span");
    value.textContent = `${(contribution.loss_fraction * 9).toFixed(2)} ${translate("inspect.points")}`;
    label.append(name, value);
    const progress = document.createElement("progress");
    progress.max = 1;
    progress.value = contribution.loss_fraction;
    progress.className = `penalty-progress penalty-${contribution.key}`;
    progress.setAttribute("aria-label", `${name.textContent}: ${value.textContent}`);
    row.append(label, progress);
    elements.penaltyList.append(row);
  }
}

function renderQuality(node) {
  elements.qualityVector.replaceChildren();
  const keys = [
    "lead_quality",
    "geometry_coverage",
    "turbulence_path_coverage",
    "cloud_path_coverage",
    "humidity_path_coverage",
    "temporal_resolution_hours",
    "quadrature_convergence",
    "approximation_length_m",
    "top_closure",
  ];
  for (const key of keys) {
    const label = translate(`qualityComponent.${key}`);
    const badge = document.createElement("details");
    badge.className = "quality-badge";
    const summary = document.createElement("summary");
    const metric = document.createElement("span");
    const value = node.quality_components[key];
    metric.textContent = `${label}: ${value === null || value === undefined ? "\u2014" : key === "temporal_resolution_hours"
      ? `${value.toFixed(value % 1 ? 1 : 0)} h`
      : key === "approximation_length_m"
        ? `${value < 0.01 ? (value * 1000).toFixed(2) + " mm" : value.toFixed(3) + " m"}`
        : `${(value * 100).toFixed(0)}%`}`;
    const help = document.createElement("span");
    help.className = "quality-help-mark";
    help.textContent = "?";
    help.setAttribute("aria-hidden", "true");
    summary.setAttribute("aria-label", translate("qualityHelp.open", { name: label }));
    summary.append(metric, help);
    const description = document.createElement("p");
    description.textContent = translate(`qualityComponent.${key}.help`);
    badge.append(summary, description);
    elements.qualityVector.append(badge);
  }
}

function renderTooltip() {
  const node = datasetView.dataset.frames[frameIndex].nodes[selectedNode];
  elements.domeTooltip.replaceChildren();
  const title = document.createElement("strong");
  title.textContent = directionLabel(node);
  const lines = [
    `${translate("inspect.overall")}: ${node.overall === null ? "\u2014" : node.overall.toFixed(1)}`,
    `${translate("inspect.factor")}: ${translate(`factor.${node.limiting_factor}`)}`,
    `${translate("inspect.seeing")}: ${metric(node.seeing_arcsec_500nm, "\u2033", 2)}`,
    `${translate("inspect.cloud")}: ${node.effective_cloud_transmission === null ? "\u2014" : `${((1 - node.effective_cloud_transmission) * 100).toFixed(1)}%`}`,
    `${translate("inspect.quality")}: ${translate(`quality.${node.data_quality}`)}`,
  ];
  elements.domeTooltip.append(title);
  for (const line of lines) {
    const item = document.createElement("div");
    item.textContent = line;
    elements.domeTooltip.append(item);
  }
  elements.domeTooltip.hidden = false;
}

function clearTooltip() {
  elements.domeTooltip.replaceChildren();
  elements.domeTooltip.hidden = true;
}

function renderDataTable() {
  if (!datasetView) {
    return;
  }
  const fragment = document.createDocumentFragment();
  const nodes = datasetView.dataset.frames[frameIndex].nodes;
  for (const [index, node] of nodes.entries()) {
    const row = document.createElement("tr");
    const directionCell = document.createElement("td");
    const button = document.createElement("button");
    button.type = "button";
    button.dataset.nodeIndex = String(index);
    const direction = directionLabel(node);
    button.textContent = direction;
    button.setAttribute("aria-label", translate("table.select", { direction }));
    directionCell.append(button);
    const overallCell = document.createElement("td");
    overallCell.textContent = node.overall === null ? "\u2014" : node.overall.toFixed(1);
    const stateCell = document.createElement("td");
    stateCell.textContent = translate(`state.${node.state}`);
    const qualityCell = document.createElement("td");
    qualityCell.textContent = translate(`quality.${node.data_quality}`);
    row.append(directionCell, overallCell, stateCell, qualityCell);
    fragment.append(row);
  }
  elements.dataTableBody.replaceChildren(fragment);
}

function updateLegend() {
  elements.layerLegend.replaceChildren();
  const layer = elements.layerSelect.value;
  for (const entry of legendForLayer(layer)) {
    const item = document.createElement("li");
    const swatch = document.createElement("canvas");
    swatch.width = 26;
    swatch.height = 18;
    swatch.setAttribute("aria-hidden", "true");
    const context = swatch.getContext("2d");
    context.fillStyle = entry.color;
    context.fillRect(0, 0, swatch.width, swatch.height);
    const label = document.createElement("span");
    if (layer === "limiting_factor") {
      label.textContent = translate(`factor.${entry.value}`);
    } else if (layer === "data_quality") {
      label.textContent = translate(`quality.${entry.value}`);
    } else {
      label.textContent = entry.value;
    }
    item.append(swatch, label);
    elements.layerLegend.append(item);
  }
}

function switchMode(mode) {
  if (mode !== "webgl" && mode !== "canvas") {
    return;
  }
  if (viewerReady && ((mode === "webgl" && !webglRenderer) || (mode === "canvas" && !canvasRenderer))) {
    return;
  }
  displayMode = mode;
  elements.webglModeButton.classList.toggle("active", mode === "webgl");
  elements.canvasModeButton.classList.toggle("active", mode === "canvas");
  elements.webglModeButton.setAttribute("aria-pressed", String(mode === "webgl"));
  elements.canvasModeButton.setAttribute("aria-pressed", String(mode === "canvas"));
  elements.modeBadge.textContent = mode === "webgl" ? "WebGL2" : "Canvas 2D";
  clearTooltip();
  updateViewControls();
  if (!viewerReady) {
    return;
  }
  setInteractiveCanvasActive(elements.domeCanvas, mode === "webgl");
  elements.overlayCanvas.dataset.active = String(mode === "webgl");
  setInteractiveCanvasActive(elements.mapCanvas, mode === "canvas");
  renderActive();
  void nextPaint().then(renderActive);
}

function setInteractiveCanvasActive(canvas, active) {
  canvas.dataset.active = String(active);
  canvas.setAttribute("aria-hidden", String(!active));
  canvas.tabIndex = active ? 0 : -1;
}

function updateViewControls() {
  const canUseWebGL = !viewerReady || Boolean(webglRenderer);
  const canUseCanvas = !viewerReady || Boolean(canvasRenderer);
  elements.webglModeButton.disabled = !canUseWebGL;
  elements.canvasModeButton.disabled = !canUseCanvas;
  const targetMode = displayMode === "webgl" ? "canvas" : "webgl";
  elements.stageModeButton.disabled = targetMode === "webgl" ? !canUseWebGL : !canUseCanvas;
  elements.stageModeLabel.textContent = targetMode === "webgl" ? "3D" : "2D";
  const label = translate(targetMode === "webgl" ? "view.switchToWebgl" : "view.switchToCanvas");
  elements.stageModeButton.setAttribute("aria-label", label);
  elements.stageModeButton.title = label;
}

async function toggleViewerExpansion() {
  const fullscreen = currentFullscreenElement();
  if (fullscreen === elements.domeStage) {
    const exit = document.exitFullscreen ?? document.webkitExitFullscreen;
    if (typeof exit === "function") {
      try {
        await exit.call(document);
      } catch {
        syncViewerExpansion();
      }
    }
    return;
  }
  if (fallbackViewerExpanded) {
    setFallbackViewerExpansion(false);
    return;
  }
  if (fullscreen) {
    return;
  }
  const request = elements.domeStage.requestFullscreen ?? elements.domeStage.webkitRequestFullscreen;
  if (typeof request === "function") {
    try {
      await request.call(elements.domeStage);
      if (currentFullscreenElement() !== elements.domeStage) {
        setFallbackViewerExpansion(true);
      }
      return;
    } catch {
      // Browser policy can reject Fullscreen API; retain an in-page expansion.
    }
  }
  setFallbackViewerExpansion(true);
}

function currentFullscreenElement() {
  return document.fullscreenElement ?? document.webkitFullscreenElement ?? null;
}

function syncViewerExpansion() {
  if (currentFullscreenElement() === elements.domeStage && fallbackViewerExpanded) {
    setFallbackViewerExpansion(false, false);
  }
  updateFullscreenControl();
  void nextPaint().then(renderActive);
}

function setFallbackViewerExpansion(expanded, restoreFocus = true) {
  const wasExpanded = fallbackViewerExpanded;
  if (expanded && !wasExpanded) {
    fallbackViewerReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  }
  fallbackViewerExpanded = expanded;
  elements.domeStage.classList.toggle("viewer-expanded-fallback", expanded);
  if (expanded) {
    elements.domeStage.setAttribute("role", "dialog");
    elements.domeStage.setAttribute("aria-modal", "true");
    elements.domeStage.setAttribute("aria-label", translate("app.title"));
    elements.domeStage.setAttribute("tabindex", "-1");
    if (!elements.domeStage.contains(document.activeElement)) {
      elements.fullscreenButton.focus({ preventScroll: true });
    }
  } else {
    elements.domeStage.removeAttribute("role");
    elements.domeStage.removeAttribute("aria-modal");
    elements.domeStage.removeAttribute("aria-label");
    elements.domeStage.removeAttribute("tabindex");
    const returnFocus = fallbackViewerReturnFocus;
    fallbackViewerReturnFocus = null;
    if (wasExpanded && restoreFocus && returnFocus?.isConnected) {
      returnFocus.focus({ preventScroll: true });
    }
  }
  updateFullscreenControl();
  void nextPaint().then(renderActive);
}

function handleViewerExpansionKeydown(event) {
  if (!fallbackViewerExpanded) {
    return;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    setFallbackViewerExpansion(false);
    return;
  }
  if (event.key !== "Tab") {
    return;
  }
  const focusable = [...elements.domeStage.querySelectorAll(
    'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
  )].filter((element) => element.getAttribute("aria-hidden") !== "true" && element.getClientRects().length > 0);
  if (focusable.length === 0) {
    event.preventDefault();
    elements.domeStage.focus({ preventScroll: true });
    return;
  }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (!elements.domeStage.contains(document.activeElement) || (event.shiftKey && document.activeElement === first)) {
    event.preventDefault();
    (event.shiftKey ? last : first).focus({ preventScroll: true });
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus({ preventScroll: true });
  }
}

function updateFullscreenControl() {
  const expanded = currentFullscreenElement() === elements.domeStage || fallbackViewerExpanded;
  document.body.classList.toggle("viewer-expanded", expanded);
  elements.fullscreenButton.classList.toggle("active", expanded);
  elements.fullscreenButton.setAttribute("aria-pressed", String(expanded));
  const label = translate(expanded ? "view.collapse" : "view.expand");
  elements.fullscreenButton.setAttribute("aria-label", label);
  elements.fullscreenButton.title = label;
  elements.fullscreenButton.disabled = !viewerReady;
}

function restoreNightModePreference() {
  try {
    return window.localStorage.getItem(NIGHT_MODE_STORAGE_KEY) === "red";
  } catch {
    return false;
  }
}

function setNightMode(enabled, persist = true) {
  redNightMode = Boolean(enabled);
  document.body.dataset.observerTheme = redNightMode ? "red" : "default";
  elements.themeColor.content = redNightMode ? "#110203" : "#07152d";
  if (persist) {
    try {
      window.localStorage.setItem(NIGHT_MODE_STORAGE_KEY, redNightMode ? "red" : "default");
    } catch {
      // Private browsing or storage policy must not disable the theme control.
    }
  }
  updateNightModeControl();
}

function updateNightModeControl() {
  elements.nightModeButton.classList.toggle("active", redNightMode);
  elements.nightModeButton.setAttribute("aria-pressed", String(redNightMode));
  const label = translate(redNightMode ? "theme.disableRed" : "theme.enableRed");
  elements.nightModeButton.setAttribute("aria-label", label);
  elements.nightModeButton.title = label;
}

function renderActive() {
  if (displayMode === "webgl") {
    webglRenderer?.render(viewController?.snapshot());
  } else {
    canvasRenderer?.render();
  }
}

function observeResize() {
  const redraw = () => renderActive();
  if ("ResizeObserver" in window) {
    resizeObserver = new ResizeObserver(redraw);
    resizeObserver.observe(elements.domeStage);
  } else {
    resizeFallback = redraw;
    window.addEventListener("resize", resizeFallback);
  }
}

function destroyRenderers() {
  viewerReady = false;
  viewController?.destroy();
  webglRenderer?.destroy();
  resizeObserver?.disconnect();
  if (resizeFallback) {
    window.removeEventListener("resize", resizeFallback);
  }
  viewController = null;
  webglRenderer = null;
  canvasRenderer = null;
  resizeObserver = null;
  resizeFallback = null;
  domeGeometry = null;
  updateViewControls();
  updateFullscreenControl();
}

function stopJobPolling() {
  jobController?.abort();
  jobController = null;
}

function showPointError(message) {
  elements.pointError.textContent = message;
  elements.pointError.hidden = false;
}

function showFatal(error, contractFailure = error instanceof ContractError) {
  elements.emptyState.hidden = true;
  elements.domeStage.hidden = true;
  elements.fatalError.hidden = false;
  elements.fatalErrorMessage.textContent = translate(contractFailure ? "error.contract" : "error.network");
  elements.fatalErrorDetail.textContent = error instanceof Error ? error.message : String(error);
}

function userError(error) {
  if (error instanceof ContractError) {
    showFatal(error, true);
    return translate("error.contract");
  }
  if (error instanceof APIError && error.code) {
    return `${translate("error.network")} (${error.code})`;
  }
  return translate("error.network");
}

function directionLabel(node) {
  if (node.azimuth_deg === null) {
    return `${translate("inspect.zenith")} \u00b7 90\u00b0`;
  }
  return `A ${node.azimuth_deg.toFixed(1)}\u00b0 \u00b7 h ${node.elevation_deg.toFixed(node.elevation_deg % 1 ? 3 : 0)}\u00b0`;
}

function metric(value, suffix, digits) {
  return value === null ? translate("inspect.notAvailable") : `${value.toFixed(digits)}${suffix}`;
}

function formatCoordinate(value) {
  return new Intl.NumberFormat(locale === "ru" ? "ru-RU" : "en-GB", {
    minimumFractionDigits: 4,
    maximumFractionDigits: 4,
  }).format(value);
}

function formatRunID(runID) {
  if (!/^\d{10}$/.test(runID)) {
    return runID;
  }
  return `${runID.slice(0, 4)}-${runID.slice(4, 6)}-${runID.slice(6, 8)} ${runID.slice(8, 10)}:00 UTC`;
}

function formatRemaining(expiresAt) {
  const seconds = Math.max(0, Math.floor((Date.parse(expiresAt) - Date.now()) / 1000));
  return formatDuration(seconds, locale);
}

function idempotencyKey() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return [...bytes].map((value) => value.toString(16).padStart(2, "0")).join("");
}

function delay(milliseconds, signal) {
  return new Promise((resolve, reject) => {
    const finish = () => {
      signal.removeEventListener("abort", abort);
      resolve();
    };
    const timer = window.setTimeout(finish, milliseconds);
    const abort = () => {
      window.clearTimeout(timer);
      reject(new DOMException("Aborted", "AbortError"));
    };
    signal.addEventListener("abort", abort, { once: true });
  });
}

function nextPaint() {
  return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

function collectElements() {
  const byID = (id) => {
    const element = document.getElementById(id);
    if (!element) {
      throw new Error(`missing required UI element #${id}`);
    }
    return element;
  };
  return {
    themeColor: byID("themeColor"),
    metaDescription: byID("metaDescription"),
    brandLink: byID("brandLink"),
    languageButton: byID("languageButton"),
    runChip: byID("runChip"),
    freshnessChip: byID("freshnessChip"),
    twilightChip: byID("twilightChip"),
    profileGreeting: byID("profileGreeting"),
    loginButton: byID("loginButton"),
    logoutButton: byID("logoutButton"),
    nightModeButton: byID("nightModeButton"),
    availabilitySignal: byID("availabilitySignal"),
    availabilityText: byID("availabilityText"),
    availabilityRun: byID("availabilityRun"),
    availabilityFreshness: byID("availabilityFreshness"),
    availabilityDetails: byID("availabilityDetails"),
    authenticationNotice: byID("authenticationNotice"),
    pointForm: byID("pointForm"),
    pointMode: [...document.querySelectorAll("input[name=pointMode]")],
    savedPointFields: byID("savedPointFields"),
    manualPointFields: byID("manualPointFields"),
    pointSelect: byID("pointSelect"),
    latitudeInput: byID("latitudeInput"),
    longitudeInput: byID("longitudeInput"),
    pointError: byID("pointError"),
    calculateButton: byID("calculateButton"),
    jobTitle: byID("jobTitle"),
    jobProgress: byID("jobProgress"),
    jobStatus: byID("jobStatus"),
    cancelJobButton: byID("cancelJobButton"),
    savedVisualizationsPanel: byID("savedVisualizationsPanel"),
    savedVisualizationsEmpty: byID("savedVisualizationsEmpty"),
    savedVisualizationsList: byID("savedVisualizationsList"),
    layerSelect: byID("layerSelect"),
    layerLegend: byID("layerLegend"),
    webglModeButton: byID("webglModeButton"),
    canvasModeButton: byID("canvasModeButton"),
    webglNotice: byID("webglNotice"),
    stageModeButton: byID("stageModeButton"),
    stageModeLabel: byID("stageModeLabel"),
    fullscreenButton: byID("fullscreenButton"),
    fatalError: byID("fatalError"),
    fatalErrorMessage: byID("fatalErrorMessage"),
    fatalErrorDetail: byID("fatalErrorDetail"),
    emptyState: byID("emptyState"),
    domeStage: byID("domeStage"),
    domeCanvas: byID("domeCanvas"),
    overlayCanvas: byID("overlayCanvas"),
    mapCanvas: byID("mapCanvas"),
    domeTooltip: byID("domeTooltip"),
    modeBadge: byID("modeBadge"),
    timeSlider: byID("timeSlider"),
    localTime: byID("localTime"),
    utcTime: byID("utcTime"),
    timePosition: byID("timePosition"),
    inspectTitle: byID("inspectTitle"),
    inspectEmpty: byID("inspectEmpty"),
    inspectContent: byID("inspectContent"),
    inspectOverall: byID("inspectOverall"),
    inspectAzimuth: byID("inspectAzimuth"),
    inspectElevation: byID("inspectElevation"),
    inspectState: byID("inspectState"),
    inspectQuality: byID("inspectQuality"),
    inspectSeeing: byID("inspectSeeing"),
    inspectTau: byID("inspectTau"),
    inspectCloud: byID("inspectCloud"),
    inspectPWV: byID("inspectPWV"),
    inspectFactor: byID("inspectFactor"),
    penaltyList: byID("penaltyList"),
    qualityVector: byID("qualityVector"),
    dataTableDetails: byID("dataTableDetails"),
    dataTableBody: byID("dataTableBody"),
  };
}
