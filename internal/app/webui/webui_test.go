package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestHandlerServesShellWithSecurityHeaders(t *testing.T) {
	t.Parallel()
	response := perform(t, NewHandler(), http.MethodGet, russianShellPath, "")
	defer closeBody(t, response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	body := readBody(t, response)
	for _, expected := range []string{"id=\"domeCanvas\"", "id=\"savedVisualizationsList\"", "max=\"71\"", "/assets/js/app.js", "10°"} {
		if !strings.Contains(body, expected) {
			t.Errorf("index does not contain %q", expected)
		}
	}
	assertHeaderContains(t, response, "Content-Type", "text/html")
	assertHeaderContains(t, response, "Content-Security-Policy", "script-src 'self'")
	assertHeaderContains(t, response, "Content-Security-Policy", "frame-ancestors 'none'")
	assertHeaderContains(t, response, "Permissions-Policy", "geolocation=()")
	assertHeaderContains(t, response, "X-Content-Type-Options", "nosniff")
	assertHeaderContains(t, response, "Cache-Control", "no-cache")
	assertHeaderContains(t, response, "Content-Language", "ru")
	assertHeaderContains(t, response, "Link", `hreflang="en"`)
	assertHeaderContains(t, response, "Link", `hreflang="ru"`)
	assertHeaderContains(t, response, "X-Robots-Tag", "noindex")
}

func TestHandlerRedirectsRootToEnglishXDefaultRoute(t *testing.T) {
	t.Parallel()
	h := NewHandler()
	for _, test := range []struct {
		header string
		want   string
	}{
		{header: "ru-RU,ru;q=0.9,en;q=0.8", want: englishShellPath},
		{header: "en-US,en;q=0.9", want: englishShellPath},
		{header: "de-DE,de;q=0.9", want: englishShellPath},
		{header: "", want: englishShellPath},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/", nil)
		request.Header.Set("Accept-Language", test.header)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != test.want {
			t.Errorf("Accept-Language %q redirect = %d %q, want %d %q",
				test.header, response.Code, response.Header().Get("Location"), http.StatusTemporaryRedirect, test.want)
		}
		if got := response.Header().Get("Vary"); got != "" {
			t.Errorf("stable root redirect unexpectedly varies by request header: %q", got)
		}
		if got := response.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
			t.Errorf("redirect X-Robots-Tag = %q", got)
		}
	}
}

func TestHandlerAssetETagAndHEAD(t *testing.T) {
	t.Parallel()
	h := NewHandler()
	first := perform(t, h, http.MethodGet, "/assets/js/app.js", "")
	defer closeBody(t, first.Body)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", first.StatusCode)
	}
	etag := first.Header.Get("ETag")
	if len(etag) != 66 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		t.Fatalf("unexpected ETag %q", etag)
	}
	assertHeaderContains(t, first, "Content-Type", "text/javascript")
	assertHeaderContains(t, first, "Cache-Control", "no-cache")
	notModified := perform(t, h, http.MethodGet, "/assets/js/app.js", etag)
	defer closeBody(t, notModified.Body)
	if notModified.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want %d", notModified.StatusCode, http.StatusNotModified)
	}
	if body := readBody(t, notModified); body != "" {
		t.Fatalf("304 response has body %q", body)
	}

	head := perform(t, h, http.MethodHead, "/assets/styles.css", "")
	defer closeBody(t, head.Body)
	if head.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d", head.StatusCode)
	}
	if body := readBody(t, head); body != "" {
		t.Fatalf("HEAD response has body %q", body)
	}
}

func TestHandlerRejectsUnknownUnsafeAndMutatingRoutes(t *testing.T) {
	t.Parallel()
	h := NewHandler()
	for _, requestPath := range []string{"/missing", "/assets/", "/assets/../index.html", "/api/v1/me"} {
		t.Run(requestPath, func(t *testing.T) {
			response := perform(t, h, http.MethodGet, requestPath, "")
			defer closeBody(t, response.Body)
			if response.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want %d", requestPath, response.StatusCode, http.StatusNotFound)
			}
		})
	}
	response := perform(t, h, http.MethodPost, "/", "")
	defer closeBody(t, response.Body)
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	if got := response.Header.Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q", got)
	}
}

func TestIndexUsesOnlyLocalExternalAssets(t *testing.T) {
	t.Parallel()
	h := NewHandler()
	response := perform(t, h, http.MethodGet, englishShellPath, "")
	defer closeBody(t, response.Body)
	body := readBody(t, response)
	for _, forbidden := range []string{"<script>", "<style", "http://", "https://", "//cdn", "<svg"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("index contains forbidden external or inline markup %q", forbidden)
		}
	}
	reference := regexp.MustCompile(`(?:href|src)="(/[^"]+)"`)
	for _, match := range reference.FindAllStringSubmatch(body, -1) {
		if !strings.HasPrefix(match[1], "/assets/") {
			continue
		}
		t.Run(match[1], func(t *testing.T) {
			assetResponse := perform(t, h, http.MethodGet, match[1], "")
			defer closeBody(t, assetResponse.Body)
			if assetResponse.StatusCode != http.StatusOK {
				t.Errorf("referenced asset %s status = %d", match[1], assetResponse.StatusCode)
			}
		})
	}
}

func TestHandlerIsSafeForConcurrentReads(t *testing.T) {
	t.Parallel()
	h := NewHandler()
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := perform(t, h, http.MethodGet, "/assets/js/geometry.js", "")
			defer closeBody(t, response.Body)
			if response.StatusCode != http.StatusOK {
				t.Errorf("status = %d", response.StatusCode)
			}
		}()
	}
	wait.Wait()
}

func TestEmbeddedESModuleGraphIsLocalAndComplete(t *testing.T) {
	t.Parallel()
	entries, err := embeddedAssets.ReadDir("assets/js")
	if err != nil {
		t.Fatal(err)
	}
	imports := regexp.MustCompile(`\bfrom\s+"([^"]+)"`)
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".js" {
			continue
		}
		name := path.Join("assets/js", entry.Name())
		content, readErr := embeddedAssets.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, match := range imports.FindAllSubmatch(content, -1) {
			reference := string(match[1])
			if !strings.HasPrefix(reference, "./") || strings.Contains(reference, "..") {
				t.Errorf("%s imports non-local module %q", name, reference)
				continue
			}
			resolved := path.Join(path.Dir(name), reference)
			module, statErr := embeddedAssets.Open(resolved)
			if statErr != nil {
				t.Errorf("%s imports missing module %q: %v", name, resolved, statErr)
			} else if closeErr := module.Close(); closeErr != nil {
				t.Errorf("close embedded module %q: %v", resolved, closeErr)
			}
		}
		for _, forbidden := range []string{"innerHTML", "outerHTML", "eval(", "new Function(", ".style."} {
			if strings.Contains(string(content), forbidden) {
				t.Errorf("%s contains unsafe DOM/code primitive %q", name, forbidden)
			}
		}
	}
}

func TestEmbeddedGeometryDescriptorsMatchPinnedDigests(t *testing.T) {
	t.Parallel()
	content, err := embeddedAssets.ReadFile("assets/js/profiles.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, test := range []struct {
		constant string
		digest   string
	}{
		{constant: "DENSE_DESCRIPTOR", digest: "e034cc4b4933ea503cd4c01c814885c8a21b2a5dd03706471885226fbdabe2bc"},
		{constant: "SPARSE_DESCRIPTOR", digest: "47f2412d8927ca7cf7691ad749cb20b4b03bc08da77a5353f0bb9459d0aa21ff"},
		{constant: "PRODUCTION_V2_DESCRIPTOR", digest: "600524c0a0ea5c21f7aaa879d3967ef007b8d5804b2217f7345703010b62af3e"},
	} {
		pattern := regexp.MustCompile(`(?s)const ` + test.constant + " = `([^`]*)`;")
		match := pattern.FindStringSubmatch(source)
		if len(match) != 2 {
			t.Fatalf("%s is missing", test.constant)
		}
		digest := sha256.Sum256([]byte(match[1]))
		if got := hex.EncodeToString(digest[:]); got != test.digest {
			t.Errorf("%s digest = %s, want %s", test.constant, got, test.digest)
		}
	}
	for _, profile := range []string{"dense-v1", "sparse-storage-v1", "production-v2"} {
		if !strings.Contains(source, `"`+profile+`": buildProfile(`) {
			t.Errorf("browser profile catalogue does not expose %q", profile)
		}
	}
}

func TestEmbeddedDatasetContractUsesPhysicalQualityUnitsAndNullableErrors(t *testing.T) {
	t.Parallel()
	contract, err := embeddedAssets.ReadFile("assets/js/contract.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contract)
	for _, catalogueField := range []string{"validateVisualizations", "item.expires_at", "item.dataset_bytes", "item.admin_fixture"} {
		if !strings.Contains(source, catalogueField) {
			t.Fatalf("browser contract does not validate saved visualization field %s", catalogueField)
		}
	}
	if !strings.Contains(source, "sparse-storage-v1|production-v2") {
		t.Fatal("browser visualization catalogue does not accept the production-v2 profile alongside legacy profiles")
	}
	if !strings.Contains(source, "if (!adminFixture && (expires <= generated || expires - generated > 96 * 60 * 60 * 1000))") {
		t.Fatal("browser visualization catalogue applies the expiring archive lifetime to the permanent administrator fixture")
	}
	for _, provenance := range []string{
		"dataset.model_product", "dataset.model_grid", "dataset.science_calibration_sha256",
	} {
		if !strings.Contains(source, provenance) {
			t.Fatalf("browser contract does not require source provenance %s", provenance)
		}
	}
	if !strings.Contains(source, "dataset.vacuum_direction_available !== false") {
		t.Fatal("browser contract does not require explicit absence of a vacuum direction")
	}
	i18n := embeddedText(t, "assets/js/i18n.js")
	for _, label := range []string{
		"Apparent altitude at aperture, 500 nm (ICON sphere)",
		"Видимая высота у апертуры, 500 нм (сфера ICON)",
	} {
		if !strings.Contains(i18n, label) {
			t.Fatalf("browser direction semantics are not visible: missing %q", label)
		}
	}
	for _, savedBoundary := range []string{
		`const INPUT_CONTRACT_VERSION = "astrodome-icon-primitives-v2"`,
		`const CURRENT_SCIENCE_VERSION = "astrodome-science-kernel-v29"`,
		`const CURRENT_SCIENCE_PATH_VERSION = "astrodome-science-path-v23"`,
		"export function validateArchivedDataset(value)",
		"return validateDatasetContract(value)",
		"if (!currentScience)",
		"dataset.input_contract_version !== INPUT_CONTRACT_VERSION",
		"dataset.refraction_version !== REFRACTION_INTEGRATOR_VERSION",
	} {
		if !strings.Contains(source, savedBoundary) {
			t.Fatalf("browser contract is missing the exact saved-result boundary %q", savedBoundary)
		}
	}
	api := embeddedText(t, "assets/js/api.js")
	if !strings.Contains(api, "return validateArchivedDataset(payload)") ||
		strings.Count(api, "return validateDataset(await getJSON(") != 1 {
		t.Fatal("browser API does not route saved results through the explicit storage boundary")
	}
	if !strings.Contains(api, `fetch("/api/v1/preferences/language"`) ||
		!strings.Contains(api, `body: JSON.stringify({ language })`) {
		t.Fatal("browser API does not persist the authenticated language preference")
	}
	app := embeddedText(t, "assets/js/app.js")
	if !strings.Contains(app, "api.savedResult(id, controller.signal)") {
		t.Fatal("saved datasets do not use the explicit saved-result validation boundary")
	}
	if !strings.Contains(app, `visualization.admin_fixture`) || !strings.Contains(app, `translate("saved.permanent")`) {
		t.Fatal("permanent admin fixture is not identified without an expiry countdown")
	}
	if !strings.Contains(app, "function formatRunID(runID)") ||
		!strings.Contains(app, "${runID.slice(0, 4)}-${runID.slice(4, 6)}-${runID.slice(6, 8)} ${runID.slice(8, 10)}:00 UTC") {
		t.Fatal("model run identifiers are not rendered as an explicit 24-hour UTC timestamp")
	}
	if !strings.Contains(app, "profile.language !== locale") ||
		!strings.Contains(app, "api.setLanguage(normalizeLocale(language)") {
		t.Fatal("saved site language is not applied or updated by the account shell")
	}
	for _, currentQualityBoundary := range []string{
		`const CURRENT_DATA_QUALITIES = new Set(["unavailable", "limited", "usable", "good"])`,
		`currentScience && quality.approximation_length_m === undefined`,
		`CURRENT_DATA_QUALITIES,`,
		`an available approximated result must be limited with zero quadrature convergence`,
	} {
		if !strings.Contains(source, currentQualityBoundary) {
			t.Fatalf("browser contract is missing the current quality boundary %q", currentQualityBoundary)
		}
	}
	if strings.Contains(source, "good_coarse_model") || strings.Contains(i18n, "good_coarse_model") {
		t.Fatal("removed good_coarse_model vocabulary is still accepted or presented by the browser")
	}
	index := embeddedText(t, "assets/index.html")
	if strings.Contains(index, `id="availabilityProfile"`) ||
		strings.Contains(i18n, `"run.profile"`) ||
		strings.Contains(app, "availabilityProfile") {
		t.Fatal("technical grid profile is still rendered in the user-facing availability card")
	}
	for _, qualityHelp := range []string{
		`document.createElement("details")`,
		`quality-help-mark`,
		`translate(` + "`qualityComponent.${key}.help`" + `)`,
		`summary.setAttribute("aria-label", translate("qualityHelp.open", { name: label }))`,
	} {
		if !strings.Contains(app, qualityHelp) {
			t.Fatalf("selected-cell quality help is missing %q", qualityHelp)
		}
	}
	if !strings.Contains(source, "quality.temporal_resolution_hours") || strings.Contains(source, "quality.temporal_resolution,") {
		t.Fatal("browser contract does not retain temporal resolution in physical hours")
	}
	for _, field := range []string{
		"numerical.overall_absolute",
		"numerical.turbulence_integral_relative",
		"numerical.integrated_cn2_relative",
		"numerical.wind_weighted_cn2_relative",
		"numerical.cloud_transmission_absolute",
	} {
		if !strings.Contains(source, "nullableFinite("+field) {
			t.Fatalf("browser contract does not permit truthful null for %s", field)
		}
	}
}

func TestEmbeddedAstrodomePresentationKeepsAProceduralAccessibleFallback(t *testing.T) {
	t.Parallel()
	index := embeddedText(t, "assets/index.html")
	if !strings.Contains(index, `<div class="sr-only" id="domeTooltip" role="status" aria-live="polite" hidden></div>`) {
		t.Error("selected-cell live status is not visually hidden while remaining accessible")
	}
	styles := embeddedText(t, "assets/styles.css")
	for _, expected := range []string{
		".dome-stage::before",
		".dome-stage::before {\n  z-index: 0;",
		"#domeCanvas {\n  z-index: 1;",
		"@keyframes astrodome-star-drift",
		"@media (prefers-reduced-motion: reduce)",
	} {
		if !strings.Contains(styles, expected) {
			t.Errorf("Astrodome styles do not contain %q", expected)
		}
	}

	webgl := embeddedText(t, "assets/js/renderer-webgl.js")
	canvas := embeddedText(t, "assets/js/renderer-canvas.js")
	if !strings.Contains(webgl, "const y = radius + 70;") {
		t.Error("WebGL compass does not reserve vertical space for the in-view controls")
	}
	if got := strings.Count(webgl, "uniform float u_alpha;"); got != 2 {
		t.Errorf("WebGL shader interface declares u_alpha %d times, want once in each shader", got)
	}
	for _, renderer := range []struct {
		name     string
		source   string
		expected []string
	}{
		{name: "WebGL", source: webgl, expected: []string{`alpha: true`, "boundaryPosition", "drawSelectedOverlay"}},
		{name: "Canvas", source: canvas, expected: []string{"drawBackdrop", "drawSelected", "outerBoundaryDeg"}},
	} {
		if strings.Contains(renderer.source, "Math.random") {
			t.Errorf("%s renderer uses a non-deterministic random star field", renderer.name)
		}
		for _, marker := range renderer.expected {
			if !strings.Contains(renderer.source, marker) {
				t.Errorf("%s renderer does not contain %q", renderer.name, marker)
			}
		}
	}
}

func TestEmbeddedSelectedCellStatusDoesNotRetainStaleFrameData(t *testing.T) {
	t.Parallel()
	app := embeddedText(t, "assets/js/app.js")
	for _, expected := range []string{
		"if (!elements.domeTooltip.hidden) {\n    renderTooltip();\n  }",
		"function clearTooltip()",
		"elements.domeTooltip.replaceChildren();\n  elements.domeTooltip.hidden = true;",
	} {
		if !strings.Contains(app, expected) {
			t.Errorf("selected-cell live status does not contain %q", expected)
		}
	}
}

func TestEmbeddedFallbackExpansionIsModalAndRestoresFocus(t *testing.T) {
	t.Parallel()
	index := embeddedText(t, "assets/index.html")
	if strings.Contains(index, `id="domeStage" role="dialog"`) || strings.Contains(index, `id="domeStage" aria-modal="true"`) {
		t.Fatal("native viewer is incorrectly modal before the fallback expansion is active")
	}
	app := embeddedText(t, "assets/js/app.js")
	for _, expected := range []string{
		`document.addEventListener("keydown", handleViewerExpansionKeydown)`,
		`elements.domeStage.setAttribute("role", "dialog")`,
		`elements.domeStage.setAttribute("aria-modal", "true")`,
		`elements.domeStage.removeAttribute("role")`,
		`elements.domeStage.removeAttribute("aria-modal")`,
		`elements.domeStage.querySelectorAll(`,
		`event.key !== "Tab"`,
		`fallbackViewerReturnFocus = document.activeElement instanceof HTMLElement`,
		`returnFocus.focus({ preventScroll: true })`,
		`setFallbackViewerExpansion(false, false)`,
	} {
		if !strings.Contains(app, expected) {
			t.Errorf("fallback viewer focus contract does not contain %q", expected)
		}
	}
}

func TestEmbeddedCanvasStarsFollowWebGLTwilightOpacity(t *testing.T) {
	t.Parallel()
	styles := embeddedText(t, "assets/styles.css")
	canvas := embeddedText(t, "assets/js/renderer-canvas.js")
	for _, opacity := range []struct {
		band  string
		value string
	}{
		{band: "day", value: ".04"},
		{band: "light_twilight", value: ".10"},
		{band: "astronomical_twilight", value: ".22"},
		{band: "astronomical_night", value: ".34"},
	} {
		if !strings.Contains(canvas, opacity.band+": "+opacity.value) {
			t.Errorf("Canvas star opacity for %s does not equal %s", opacity.band, opacity.value)
		}
		if !strings.Contains(styles, "--dome-star-opacity: "+opacity.value) {
			t.Errorf("WebGL star opacity does not contain %s for comparison", opacity.value)
		}
	}
	if !strings.Contains(canvas, "const alpha = starOpacity * (.42 + bright * .53)") {
		t.Error("Canvas star alpha does not use the twilight-dependent opacity")
	}
}

func TestEmbeddedJobStatesMatchDirectionalBackend(t *testing.T) {
	t.Parallel()
	contract := embeddedText(t, "assets/js/contract.js")
	for _, state := range []string{`"queued"`, `"running"`, `"ready"`, `"failed"`, `"cancelled"`} {
		if !strings.Contains(contract, state) {
			t.Errorf("browser job contract does not contain backend state %s", state)
		}
	}
	for _, source := range []string{
		contract,
		embeddedText(t, "assets/js/app.js"),
		embeddedText(t, "assets/js/i18n.js"),
	} {
		if strings.Contains(source, "superseded") {
			t.Error("browser exposes superseded, which is not a directional backend state")
		}
	}
}

func TestEmbeddedAvailabilityUsesLiveWorkerHealthAndPolling(t *testing.T) {
	app := embeddedText(t, "assets/js/app.js")
	for _, marker := range []string{
		"AVAILABILITY_POLL_INTERVAL_MS = 10_000",
		"visibilitychange",
		"refreshAvailability()",
		"availability.workerAvailable",
		"availabilityReasonMessage",
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("availability UI does not contain %q", marker)
		}
	}
	contract := embeddedText(t, "assets/js/contract.js")
	if !strings.Contains(contract, `boolean(availability.worker_available, "$.worker_available")`) {
		t.Fatal("browser availability contract does not require explicit worker health")
	}
	i18n := embeddedText(t, "assets/js/i18n.js")
	for _, message := range []string{"availability.modelPreparing", "availability.workerDown", "availability.statusUnavailable"} {
		if strings.Count(i18n, message) != 2 {
			t.Errorf("availability message %q is not present in both locales", message)
		}
	}
}

func TestEmbeddedLanguageRoutesAvoidLocaleAndAuthenticationFlash(t *testing.T) {
	t.Parallel()
	index := embeddedText(t, "assets/index.html")
	for _, marker := range []string{
		`data-language-ready="false"`,
		`data-session-state="pending"`,
		`id="languageButton"`,
		`data-session-view="anonymous"`,
		`data-session-view="authenticated"`,
		`name="robots" content="noindex,nofollow,noarchive"`,
	} {
		if !strings.Contains(index, marker) {
			t.Errorf("localized shell does not contain %q", marker)
		}
	}
	app := embeddedText(t, "assets/js/app.js")
	for _, marker := range []string{
		"localeFromPath(window.location.pathname)",
		"localizedShellPath(alternateLocale)",
		`dataset.languageReady = "true"`,
		`dataset.sessionState = "authenticated"`,
		`dataset.sessionState = "anonymous"`,
		`/auth/telegram/login?lang=`,
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("localized bootstrap does not contain %q", marker)
		}
	}
	if strings.Contains(app, "locale = profile.language") {
		t.Fatal("the session language overrides the explicit language URL")
	}
	styles := embeddedText(t, "assets/styles.css")
	for _, marker := range []string{
		`html[data-language-ready="false"] body > :not(.boot-splash)`,
		`html[data-session-state="pending"] [data-session-view]`,
	} {
		if !strings.Contains(styles, marker) {
			t.Errorf("flash-prevention CSS does not contain %q", marker)
		}
	}
	i18n := embeddedText(t, "assets/js/i18n.js")
	for _, key := range []string{"app.description", "skip.main", "language.switchTo.ru", "language.switchTo.en", "job.title"} {
		if strings.Count(i18n, key) != 2 {
			t.Errorf("translation key %q is not present in both locales", key)
		}
	}
}

func TestEmbeddedAvailabilityPollsAtomicWorkerAndModelStatus(t *testing.T) {
	app := embeddedText(t, "assets/js/app.js")
	for _, expected := range []string{
		"AVAILABILITY_POLL_INTERVAL_MS = 10_000",
		`document.addEventListener("visibilitychange"`,
		"await refreshAvailability()",
		"availability.workerAvailable",
		"availabilityReasonMessage",
	} {
		if !strings.Contains(app, expected) {
			t.Errorf("availability UI does not contain %q", expected)
		}
	}
	contract := embeddedText(t, "assets/js/contract.js")
	if !strings.Contains(contract, `boolean(availability.worker_available, "$.worker_available")`) {
		t.Fatal("browser availability contract does not require explicit worker health")
	}
	translations := embeddedText(t, "assets/js/i18n.js")
	for _, expected := range []string{"availability.modelPreparing", "availability.statusUnavailable"} {
		if strings.Count(translations, expected) != 2 {
			t.Errorf("availability translation %q is not present in both locales", expected)
		}
	}
}

func TestEmbeddedViewerControlsSupportReversibleModesAndExpansion(t *testing.T) {
	t.Parallel()
	index := embeddedText(t, "assets/index.html")
	for _, marker := range []string{
		`id="stageModeButton"`,
		`id="fullscreenButton"`,
		`id="webglModeButton" data-i18n="view.webgl" aria-pressed="true">`,
		`id="canvasModeButton" data-i18n="view.canvas" aria-pressed="false">`,
		`id="domeCanvas" tabindex="0"`,
		`id="mapCanvas" tabindex="-1"`,
	} {
		if !strings.Contains(index, marker) {
			t.Errorf("Astrodome shell does not contain %q", marker)
		}
	}

	app := embeddedText(t, "assets/js/app.js")
	for _, marker := range []string{
		`elements.webglModeButton.addEventListener("click", () => switchMode("webgl"))`,
		`elements.canvasModeButton.addEventListener("click", () => switchMode("canvas"))`,
		`switchMode(displayMode === "webgl" ? "canvas" : "webgl")`,
		`if (viewerReady && ((mode === "webgl" && !webglRenderer) || (mode === "canvas" && !canvasRenderer)))`,
		`displayMode = mode`,
		`elements.webglModeButton.classList.toggle("active", mode === "webgl")`,
		`elements.canvasModeButton.classList.toggle("active", mode === "canvas")`,
		`const canUseWebGL = !viewerReady || Boolean(webglRenderer)`,
		`const canUseCanvas = !viewerReady || Boolean(canvasRenderer)`,
		`if (!viewerReady)`,
		`setInteractiveCanvasActive(elements.domeCanvas, mode === "webgl")`,
		`setInteractiveCanvasActive(elements.mapCanvas, mode === "canvas")`,
		`elements.domeStage.requestFullscreen ?? elements.domeStage.webkitRequestFullscreen`,
		`setFallbackViewerExpansion(true)`,
		`document.addEventListener("fullscreenchange", syncViewerExpansion)`,
		`elements.fullscreenButton.disabled = !viewerReady`,
		`void nextPaint().then(renderActive)`,
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("Astrodome presentation controller does not contain %q", marker)
		}
	}
	if strings.Contains(app, "elements.domeCanvas.hidden") || strings.Contains(app, "elements.mapCanvas.hidden") {
		t.Error("mode switching still removes a renderer canvas from layout")
	}
	if strings.Contains(index, `id="stageModeButton" type="button" aria-label="Switch to the accessible 2D map" title="Switch to the accessible 2D map" disabled`) {
		t.Error("compact mode toggle is disabled before a dataset is loaded")
	}

	styles := embeddedText(t, "assets/styles.css")
	for _, marker := range []string{
		".dome-stage:fullscreen",
		".dome-stage.viewer-expanded-fallback",
		`#domeCanvas[data-active="false"]`,
		"opacity: 0",
		"height: 100dvh",
	} {
		if !strings.Contains(styles, marker) {
			t.Errorf("Astrodome expansion styles do not contain %q", marker)
		}
	}
}

func TestEmbeddedRedNightModePersistsAndPreservesLegendMapping(t *testing.T) {
	t.Parallel()
	index := embeddedText(t, "assets/index.html")
	if !strings.Contains(index, `id="nightModeButton"`) || !strings.Contains(index, `aria-pressed="false"`) {
		t.Fatal("red night-mode control is missing its accessible initial state")
	}

	app := embeddedText(t, "assets/js/app.js")
	for _, marker := range []string{
		`NIGHT_MODE_STORAGE_KEY = "astrosferum.red-night-mode"`,
		`restoreNightModePreference()`,
		`window.localStorage.getItem(NIGHT_MODE_STORAGE_KEY) === "red"`,
		`window.localStorage.setItem(NIGHT_MODE_STORAGE_KEY, redNightMode ? "red" : "default")`,
		`elements.nightModeButton.setAttribute("aria-pressed", String(redNightMode))`,
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("red night-mode controller does not contain %q", marker)
		}
	}

	styles := embeddedText(t, "assets/styles.css")
	for _, selector := range []string{
		`body[data-observer-theme="red"] #domeCanvas`,
		`body[data-observer-theme="red"] #overlayCanvas`,
		`body[data-observer-theme="red"] #mapCanvas`,
		`body[data-observer-theme="red"] .legend canvas`,
		`body[data-observer-theme="red"] .job-progress span`,
	} {
		if !strings.Contains(styles, selector) {
			t.Errorf("red night-mode presentation does not contain %q", selector)
		}
	}
	if !strings.Contains(styles, "grayscale(1) sepia(1)") {
		t.Error("red night mode does not use a presentation-only monochrome mapping")
	}

	translations := embeddedText(t, "assets/js/i18n.js")
	for _, key := range []string{"theme.red", "theme.enableRed", "theme.disableRed"} {
		if strings.Count(translations, key) != 2 {
			t.Errorf("night-mode translation %q is not present in both locales", key)
		}
	}
}

func TestEmbeddedCoordinateModeLabelIsExplicitlyCentered(t *testing.T) {
	t.Parallel()
	styles := embeddedText(t, "assets/styles.css")
	segment := regexp.MustCompile(`(?s)\.segmented span\s*\{([^}]*)\}`).FindStringSubmatch(styles)
	if len(segment) != 2 {
		t.Fatal("segmented control label style is missing")
	}
	for _, declaration := range []string{"width: 100%", "justify-content: center", "text-align: center"} {
		if !strings.Contains(segment[1], declaration) {
			t.Errorf("segmented control labels are missing %q", declaration)
		}
	}
}

func TestDatasetClientLimitMatchesServerArchiveContract(t *testing.T) {
	t.Parallel()
	api := embeddedText(t, "assets/js/api.js")
	if !strings.Contains(api, "const DATASET_RESPONSE_LIMIT = 128 * 1024 * 1024;") {
		t.Fatal("browser dataset limit no longer matches the 128 MiB server archive contract")
	}
}

func TestSavedVisualizationRetentionMatchesServerContract(t *testing.T) {
	t.Parallel()
	contract := embeddedText(t, "assets/js/contract.js")
	if !strings.Contains(contract, "expires - generated > 96 * 60 * 60 * 1000") {
		t.Fatal("browser retention validation no longer matches the 96-hour server contract")
	}
	translations := embeddedText(t, "assets/js/i18n.js")
	if strings.Count(translations, "96 hours") != 1 || strings.Count(translations, "96 часов") != 1 {
		t.Fatal("saved-visualization retention is not explicit in both locales")
	}
}

func embeddedText(t *testing.T, name string) string {
	t.Helper()
	content, err := embeddedAssets.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func perform(t *testing.T, handler http.Handler, method, target, etag string) *http.Response {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Result()
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(content)
}

func closeBody(t *testing.T, body io.Closer) {
	t.Helper()
	if err := body.Close(); err != nil {
		t.Errorf("close response body: %v", err)
	}
}

func assertHeaderContains(t *testing.T, response *http.Response, name, expected string) {
	t.Helper()
	if actual := response.Header.Get(name); !strings.Contains(actual, expected) {
		t.Errorf("%s = %q, want it to contain %q", name, actual, expected)
	}
}
