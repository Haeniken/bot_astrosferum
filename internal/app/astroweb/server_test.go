package astroweb

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testPoints struct{ point SavedPoint }

func (points testPoints) WebPoints(context.Context, int64) ([]SavedPoint, error) {
	return []SavedPoint{points.point}, nil
}

func (points testPoints) WebPoint(_ context.Context, userID, pointID int64) (SavedPoint, error) {
	if userID != 42 || pointID != points.point.ID {
		return SavedPoint{}, ErrJobNotFound
	}
	return points.point, nil
}

type testGateway struct {
	availabilityUserID int64
	admission          AstrodomeAdmission
	canceled           bool
}

const testGatewayDataset = `{"frames":[{"nodes":[{"state":"valid"}]}]}`

func (gateway *testGateway) Availability(_ context.Context, userID int64) (AstrodomeAvailability, error) {
	gateway.availabilityUserID = userID
	return AstrodomeAvailability{Enabled: true, Available: true, WorkerAvailable: true, Provider: "icon-eu", RunID: "2026072812", GridProfile: "dense-v1"}, nil
}

func (gateway *testGateway) Admit(_ context.Context, admission AstrodomeAdmission) (AstrodomeJobStatus, error) {
	gateway.admission = admission
	return AstrodomeJobStatus{ID: "job_abcdefghijklmnopqrstuvwxyz", State: "queued"}, nil
}

func (gateway *testGateway) Status(_ context.Context, userID int64, jobID string) (AstrodomeJobStatus, error) {
	if userID != 42 || jobID != "job_abcdefghijklmnopqrstuvwxyz" {
		return AstrodomeJobStatus{}, ErrJobNotFound
	}
	return AstrodomeJobStatus{ID: jobID, State: "ready", DatasetBytes: 2}, nil
}

func (gateway *testGateway) Dataset(_ context.Context, userID int64, jobID string) (AstrodomeDataset, error) {
	if userID != 42 || jobID != "job_abcdefghijklmnopqrstuvwxyz" {
		return AstrodomeDataset{}, ErrJobNotFound
	}
	return AstrodomeDataset{
		Body: io.NopCloser(strings.NewReader(testGatewayDataset)), Bytes: int64(len(testGatewayDataset)), ETag: `"dataset"`,
	}, nil
}

func (gateway *testGateway) Cancel(_ context.Context, userID int64, jobID string) error {
	if userID != 42 || jobID != "job_abcdefghijklmnopqrstuvwxyz" {
		return ErrJobNotFound
	}
	gateway.canceled = true
	return nil
}

type readyOK struct{ err error }

func (ready readyOK) Ready(context.Context) error { return ready.err }

func newTestServer(t *testing.T) (*Server, *OIDCAuthenticator, *testGateway, string, string) {
	return newTestServerWithAccess(t, true, nil)
}

func newTestServerWithAccess(t *testing.T, public bool, adminIDs []int64) (*Server, *OIDCAuthenticator, *testGateway, string, string) {
	t.Helper()
	store := newMemoryAuthStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	auth := &OIDCAuthenticator{
		store: store, publicOrigin: "https://astrosferum.com", csrfKey: []byte(strings.Repeat("c", 32)),
		now: func() time.Time { return now },
	}
	sessionToken := "test-session-token-with-enough-entropy"
	if err := store.CreateWebSession(context.Background(), WebSession{
		TokenHash: sha256.Sum256([]byte(sessionToken)), TelegramUserID: 42, Language: "ru",
		OIDCIssuer: telegramIssuer, OIDCSubject: "subject-42",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	gateway := &testGateway{}
	visualizations, err := NewVisualizationCatalog(
		t.TempDir(), newMemoryVisualizationStore(), gateway, func() time.Time { return now },
		func([]byte) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	edge := strings.Repeat("e", 32)
	server, err := NewServer(ServerConfig{
		PublicOrigin: "https://astrosferum.com", EdgeCredential: []byte(edge),
		AstrodomePublic: public, AstrodomeAdminIDs: adminIDs, Authenticator: auth,
		Points:  testPoints{point: SavedPoint{ID: 7, Name: "Test", Latitude: 59.9, Longitude: 30.2}},
		Gateway: gateway, Visualizations: visualizations, Readiness: readyOK{}, Static: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "static")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, auth, gateway, edge, sessionToken
}

func installServerAdminFixture(t *testing.T, server *Server) string {
	t.Helper()
	catalog := server.config.Visualizations
	store, ok := catalog.store.(*memoryVisualizationStore)
	if !ok {
		t.Fatal("test server visualization store has an unexpected type")
	}
	fileName, bytesWritten, digest, etag, err := catalog.writeDataset(t.Context(), AstrodomeDataset{
		Body: io.NopCloser(strings.NewReader(testGatewayDataset)),
	})
	if err != nil {
		t.Fatal(err)
	}
	const fixtureID = "viz_0123456789abcdef0123456789abcdef"
	now := catalog.now().UTC()
	store.rows[fixtureID] = visualizationMemoryRow{owner: 42, ready: &AstrodomeVisualization{
		ID: fixtureID, Name: "Плавск", Latitude: 53.650005, Longitude: 37.346192,
		Provider: "icon-eu", RunID: "2026080900", GridProfile: "production-v2",
		GeometryDigest: "sha256:fixture", DatasetBytes: bytesWritten,
		GeneratedAt: now, ExpiresAt: now.Add(-time.Hour), AdminFixture: true,
		SourceJobID: "job_fixtureabcdefghijklmnopqr", DatasetFile: fileName, DatasetSHA256: digest, ETag: etag,
	}}
	return fixtureID
}

func TestServerPublicFixtureAccessIsAnonymousOrAdministratorOnly(t *testing.T) {
	t.Run("anonymous visitor can open the permanent fixture while rollout is disabled", func(t *testing.T) {
		server, auth, _, edge, token := newTestServerWithAccess(t, false, []int64{42})
		fixtureID := installServerAdminFixture(t, server)

		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/visualizations", nil)
		request.Header.Set(edgeHeader, edge)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		var catalogue struct {
			Visualizations []AstrodomeVisualization `json:"visualizations"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &catalogue) != nil ||
			len(catalogue.Visualizations) != 1 || catalogue.Visualizations[0].ID != fixtureID || !catalogue.Visualizations[0].AdminFixture {
			t.Fatalf("anonymous fixture catalogue = %d %q", response.Code, response.Body.String())
		}

		request = httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"http://origin/api/v1/astrodome/visualizations/"+fixtureID+"/dataset", nil)
		request.Header.Set(edgeHeader, edge)
		response = httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("anonymous fixture dataset = %d encoding=%q", response.Code, response.Header().Get("Content-Encoding"))
		}

		request = authenticatedRequest(t.Context(), http.MethodGet,
			"http://origin/api/v1/astrodome/visualizations/"+fixtureID+"/dataset", edge, token, auth.csrfToken(token), "")
		response = httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("administrator fixture dataset = %d %q", response.Code, response.Body.String())
		}
	})

	t.Run("authenticated non-admin cannot discover or open the fixture", func(t *testing.T) {
		server, auth, _, edge, token := newTestServerWithAccess(t, true, []int64{7})
		fixtureID := installServerAdminFixture(t, server)
		csrf := auth.csrfToken(token)

		request := authenticatedRequest(t.Context(), http.MethodGet,
			"http://origin/api/v1/astrodome/visualizations", edge, token, csrf, "")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		var catalogue struct {
			Visualizations []AstrodomeVisualization `json:"visualizations"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &catalogue) != nil || len(catalogue.Visualizations) != 0 {
			t.Fatalf("non-admin fixture catalogue = %d %q", response.Code, response.Body.String())
		}

		request = authenticatedRequest(t.Context(), http.MethodGet,
			"http://origin/api/v1/astrodome/visualizations/"+fixtureID+"/dataset", edge, token, csrf, "")
		response = httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("non-admin fixture dataset = %d %q", response.Code, response.Body.String())
		}
	})
}

func TestServerAstrodomePublicAndAdminAuthorization(t *testing.T) {
	t.Run("admin preview while public rollout is disabled", func(t *testing.T) {
		server, auth, gateway, edge, token := newTestServerWithAccess(t, false, []int64{42})
		request := authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/availability", edge, token, auth.csrfToken(token), "")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK || gateway.availabilityUserID != 42 {
			t.Fatalf("admin preview response = %d %q, user = %d", response.Code, response.Body.String(), gateway.availabilityUserID)
		}
	})

	for _, test := range []struct {
		name     string
		adminIDs []int64
	}{
		{name: "empty admins deny all"},
		{name: "non-admin is denied", adminIDs: []int64{7}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, auth, gateway, edge, token := newTestServerWithAccess(t, false, test.adminIDs)
			csrf := auth.csrfToken(token)
			requests := []*http.Request{
				authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/availability", edge, token, csrf, ""),
				authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/visualizations", edge, token, csrf, ""),
				authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/visualizations/viz_0123456789abcdef0123456789abcdef/dataset", edge, token, csrf, ""),
				authenticatedRequest(t.Context(), http.MethodPost, "http://origin/api/v1/astrodome/jobs", edge, token, csrf, `{"point_id":7,"idempotency_key":"0123456789abcdef"}`),
				authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/jobs/job_abcdefghijklmnopqrstuvwxyz", edge, token, csrf, ""),
				authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/jobs/job_abcdefghijklmnopqrstuvwxyz/dataset", edge, token, csrf, ""),
				authenticatedRequest(t.Context(), http.MethodDelete, "http://origin/api/v1/astrodome/jobs/job_abcdefghijklmnopqrstuvwxyz", edge, token, csrf, ""),
			}
			requests[1].Header.Set("Content-Type", "application/json")
			for _, request := range requests {
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"astrodome_disabled"`) {
					t.Fatalf("%s response = %d %q", request.Method+" "+request.URL.Path, response.Code, response.Body.String())
				}
			}
			if gateway.availabilityUserID != 0 || gateway.admission.TelegramUserID != 0 || gateway.canceled {
				t.Fatalf("denied request reached gateway: %+v", gateway)
			}
		})
	}
}

func authenticatedRequest(ctx context.Context, method, target, edge, sessionToken, csrf, body string) *http.Request {
	request := httptest.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	request.Header.Set(edgeHeader, edge)
	request.Header.Set("Origin", "https://astrosferum.com")
	request.Header.Set("X-Astrosferum-CSRF", csrf)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
	return request
}

func TestServerOriginBoundaryAndHealth(t *testing.T) {
	server, _, _, edge, _ := newTestServer(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://origin/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://origin/readyz", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("request without edge credential status = %d", response.Code)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://origin/readyz", nil)
	request.Header.Set(edgeHeader, edge)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatalf("ready response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers missing")
	}
}

func TestServerMeAndAdmissionOwnership(t *testing.T) {
	server, auth, gateway, edge, token := newTestServer(t)
	csrf := auth.csrfToken(token)
	request := authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/me", edge, token, csrf, "")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"telegram_user_id":42`) || !strings.Contains(response.Body.String(), csrf) {
		t.Fatalf("me response = %d %q", response.Code, response.Body.String())
	}

	body := `{"point_id":7,"idempotency_key":"0123456789abcdef"}`
	request = authenticatedRequest(t.Context(), http.MethodPost, "http://origin/api/v1/astrodome/jobs", edge, token, csrf, body)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || gateway.admission.TelegramUserID != 42 || gateway.admission.Point.ID != 7 {
		t.Fatalf("admission response = %d %q, admission=%+v", response.Code, response.Body.String(), gateway.admission)
	}

	for _, invalid := range []string{
		`{"point_id":7,"coordinates":{"latitude":59,"longitude":30},"idempotency_key":"0123456789abcdef"}`,
		`{"idempotency_key":"0123456789abcdef"}`,
		`{"point_id":999,"idempotency_key":"0123456789abcdef"}`,
	} {
		request = authenticatedRequest(t.Context(), http.MethodPost, "http://origin/api/v1/astrodome/jobs", edge, token, csrf, invalid)
		response = httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
			t.Fatalf("invalid admission %q status = %d", invalid, response.Code)
		}
	}
}

func TestServerPersistsAuthenticatedLanguagePreference(t *testing.T) {
	server, auth, _, edge, token := newTestServer(t)
	csrf := auth.csrfToken(token)
	request := authenticatedRequest(
		t.Context(),
		http.MethodPut,
		"http://origin/api/v1/preferences/language",
		edge,
		token,
		csrf,
		`{"language":"en"}`,
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("language update response = %d %q", response.Code, response.Body.String())
	}
	request = authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/me", edge, token, csrf, "")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"language":"en"`) {
		t.Fatalf("updated profile response = %d %q", response.Code, response.Body.String())
	}

	request = authenticatedRequest(
		t.Context(), http.MethodPut, "http://origin/api/v1/preferences/language", edge, token, csrf, `{"language":"de"}`,
	)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsupported language response = %d %q", response.Code, response.Body.String())
	}

	request = authenticatedRequest(
		t.Context(), http.MethodPut, "http://origin/api/v1/preferences/language", edge, token, "invalid", `{"language":"ru"}`,
	)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("invalid CSRF language response = %d %q", response.Code, response.Body.String())
	}

	store := auth.store.(*memoryAuthStore)
	store.mu.Lock()
	store.updateLanguageErr = errors.New("database unavailable")
	store.mu.Unlock()
	request = authenticatedRequest(
		t.Context(), http.MethodPut, "http://origin/api/v1/preferences/language", edge, token, csrf, `{"language":"ru"}`,
	)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "preference_unavailable") {
		t.Fatalf("storage failure language response = %d %q", response.Code, response.Body.String())
	}
}

func TestServerJobIsolationDatasetAndCancellation(t *testing.T) {
	server, auth, gateway, edge, token := newTestServer(t)
	csrf := auth.csrfToken(token)
	jobID := "job_abcdefghijklmnopqrstuvwxyz"
	request := authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/jobs/"+jobID, edge, token, csrf, "")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"ready"`) {
		t.Fatalf("status response = %d %q", response.Code, response.Body.String())
	}
	request = authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/jobs/"+jobID+"/dataset", edge, token, csrf, "")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != testGatewayDataset || response.Header().Get("ETag") != `"dataset"` {
		t.Fatalf("dataset response = %d %q", response.Code, response.Body.String())
	}
	request = authenticatedRequest(t.Context(), http.MethodDelete, "http://origin/api/v1/astrodome/jobs/"+jobID, edge, token, csrf, "")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !gateway.canceled {
		t.Fatalf("cancel response = %d canceled=%t", response.Code, gateway.canceled)
	}
	request = authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/jobs/short", edge, token, csrf, "")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("invalid job ID status = %d", response.Code)
	}
}

func TestServerArchivesAndReopensCompletedVisualization(t *testing.T) {
	server, auth, _, edge, token := newTestServer(t)
	csrf := auth.csrfToken(token)
	request := authenticatedRequest(t.Context(), http.MethodPost, "http://origin/api/v1/astrodome/jobs", edge, token, csrf,
		`{"point_id":7,"idempotency_key":"0123456789abcdef"}`)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("admit response = %d %q", response.Code, response.Body.String())
	}
	jobID := "job_abcdefghijklmnopqrstuvwxyz"
	request = authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/jobs/"+jobID, edge, token, csrf, "")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready response = %d %q", response.Code, response.Body.String())
	}

	request = authenticatedRequest(t.Context(), http.MethodGet, "http://origin/api/v1/astrodome/visualizations", edge, token, csrf, "")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var catalogue struct {
		Visualizations []AstrodomeVisualization `json:"visualizations"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &catalogue) != nil || len(catalogue.Visualizations) != 1 {
		t.Fatalf("catalogue response = %d %q", response.Code, response.Body.String())
	}
	request = authenticatedRequest(t.Context(), http.MethodGet,
		"http://origin/api/v1/astrodome/visualizations/"+catalogue.Visualizations[0].ID+"/dataset", edge, token, csrf, "")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("saved dataset response = %d encoding=%q", response.Code, response.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(response.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(content) != testGatewayDataset {
		t.Fatalf("saved dataset = %q, err=%v", content, err)
	}
}

func TestServerRejectsCSRFAndUnknownJSON(t *testing.T) {
	server, auth, _, edge, token := newTestServer(t)
	request := authenticatedRequest(t.Context(), http.MethodPost, "http://origin/api/v1/astrodome/jobs", edge, token, "wrong", `{"point_id":7,"idempotency_key":"0123456789abcdef"}`)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("invalid CSRF status = %d", response.Code)
	}
	request = authenticatedRequest(t.Context(), http.MethodPost, "http://origin/api/v1/astrodome/jobs", edge, token, auth.csrfToken(token), `{"point_id":7,"idempotency_key":"0123456789abcdef","extra":true}`)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field status = %d", response.Code)
	}
}

func TestReadyFailureIsBooleanOnly(t *testing.T) {
	server, _, _, edge, _ := newTestServer(t)
	server.config.Readiness = readyOK{err: errors.New("database password leaked here")}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://origin/readyz", nil)
	request.Header.Set(edgeHeader, edge)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "password") {
		t.Fatalf("ready failure leaked detail: %d %q", response.Code, response.Body.String())
	}
}
