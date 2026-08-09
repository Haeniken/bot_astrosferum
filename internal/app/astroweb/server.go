package astroweb

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxAPIRequestBytes = 16 << 10
	edgeHeader         = "X-Astrosferum-Edge"
)

var (
	ErrJobNotFound          = errors.New("astrodome job not found")
	ErrDirectionalBusy      = errors.New("directional queue is full")
	ErrAstrodomeDisabled    = errors.New("astrodome analysis is disabled")
	ErrAstrodomeUnavailable = errors.New("astrodome analysis is unavailable")
)

type SavedPoint struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type PointStore interface {
	WebPoints(context.Context, int64) ([]SavedPoint, error)
	WebPoint(context.Context, int64, int64) (SavedPoint, error)
}

type AstrodomeAvailability struct {
	Enabled          bool      `json:"enabled"`
	Available        bool      `json:"available"`
	WorkerAvailable  bool      `json:"worker_available"`
	Provider         string    `json:"provider,omitempty"`
	RunID            string    `json:"run_id,omitempty"`
	RunBaseTime      time.Time `json:"run_base_time,omitempty"`
	FreshnessSeconds int64     `json:"freshness_seconds,omitempty"`
	Stale            bool      `json:"stale"`
	GridProfile      string    `json:"grid_profile,omitempty"`
	QueueLength      int       `json:"queue_length"`
	Running          bool      `json:"running"`
	Reason           string    `json:"reason,omitempty"`
}

type AstrodomeAdmission struct {
	TelegramUserID int64
	Point          SavedPoint
	Language       string
	IdempotencyKey string
}

type AstrodomeJobStatus struct {
	ID             string     `json:"id"`
	State          string     `json:"state"`
	QueuePosition  *int       `json:"queue_position"`
	EstimatedAt    *time.Time `json:"estimated_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Provider       string     `json:"provider,omitempty"`
	RunID          string     `json:"run_id,omitempty"`
	GridProfile    string     `json:"grid_profile,omitempty"`
	GeometryDigest string     `json:"grid_geometry_digest,omitempty"`
	DatasetBytes   int64      `json:"dataset_bytes,omitempty"`
	FailureCode    string     `json:"failure_code,omitempty"`
}

type AstrodomeDataset struct {
	Body            io.ReadCloser
	Bytes           int64
	ETag            string
	ContentEncoding string
}

type AstrodomeGateway interface {
	Availability(context.Context, int64) (AstrodomeAvailability, error)
	Admit(context.Context, AstrodomeAdmission) (AstrodomeJobStatus, error)
	Status(context.Context, int64, string) (AstrodomeJobStatus, error)
	Dataset(context.Context, int64, string) (AstrodomeDataset, error)
	Cancel(context.Context, int64, string) error
}

type Readiness interface {
	Ready(context.Context) error
}

type ServerConfig struct {
	PublicOrigin      string
	EdgeCredential    []byte
	AstrodomePublic   bool
	AstrodomeAdminIDs []int64
	Authenticator     *OIDCAuthenticator
	Points            PointStore
	Gateway           AstrodomeGateway
	Visualizations    *VisualizationCatalog
	Readiness         Readiness
	Static            http.Handler
	Logger            *slog.Logger
}

type Server struct {
	config ServerConfig
	mux    *http.ServeMux
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.PublicOrigin == "" || config.Authenticator == nil || config.Points == nil || config.Gateway == nil ||
		config.Visualizations == nil || config.Readiness == nil || config.Static == nil {
		return nil, errors.New("web server origin, auth, points, gateway, visualizations, readiness, and static handler are required")
	}
	if len(config.EdgeCredential) < minimumOpaqueCredentialBytes {
		return nil, errors.New("edge credential must contain at least 32 bytes")
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	config.AstrodomeAdminIDs = append([]int64(nil), config.AstrodomeAdminIDs...)
	server := &Server{config: config, mux: http.NewServeMux()}
	server.routes()
	return server, nil
}

func (server *Server) routes() {
	server.mux.HandleFunc("GET /healthz", server.health)
	server.mux.HandleFunc("GET /readyz", server.ready)
	server.mux.HandleFunc("GET /auth/telegram/login", server.login)
	server.mux.HandleFunc("GET /auth/telegram/callback", server.callback)
	server.mux.HandleFunc("GET /api/v1/me", server.me)
	server.mux.HandleFunc("PUT /api/v1/preferences/language", server.languagePreference)
	server.mux.HandleFunc("POST /api/v1/logout", server.logout)
	server.mux.HandleFunc("GET /api/v1/points", server.points)
	server.mux.HandleFunc("GET /api/v1/astrodome/availability", server.availability)
	server.mux.HandleFunc("GET /api/v1/astrodome/visualizations", server.visualizations)
	server.mux.HandleFunc("GET /api/v1/astrodome/visualizations/{visualizationID}/dataset", server.visualizationDataset)
	server.mux.HandleFunc("POST /api/v1/astrodome/jobs", server.admit)
	server.mux.HandleFunc("GET /api/v1/astrodome/jobs/{jobID}", server.status)
	server.mux.HandleFunc("DELETE /api/v1/astrodome/jobs/{jobID}", server.cancel)
	server.mux.HandleFunc("GET /api/v1/astrodome/jobs/{jobID}/dataset", server.dataset)
	server.mux.Handle("/", server.config.Static)
}

func (server *Server) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(w)
	if request.URL.Path != "/healthz" && !server.validEdge(request.Header.Get(edgeHeader)) {
		http.NotFound(w, request)
		return
	}
	server.mux.ServeHTTP(w, request)
}

func (server *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (server *Server) ready(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := server.config.Readiness.Ready(ctx); err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "not_ready")
		return
	}
	if err := server.config.Visualizations.Ready(ctx); err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "not_ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ready": true})
}

func (server *Server) login(w http.ResponseWriter, request *http.Request) {
	if err := server.config.Authenticator.Begin(w, request); err != nil {
		server.logError("begin OIDC login", err)
		writeProblem(w, http.StatusServiceUnavailable, "login_unavailable")
	}
}

func (server *Server) callback(w http.ResponseWriter, request *http.Request) {
	if err := server.config.Authenticator.Callback(w, request); err != nil {
		// Never log RawQuery, Referer, cookies, code, or state.
		server.logError("complete OIDC callback", err)
		writeProblem(w, http.StatusUnauthorized, "login_failed")
	}
}

func (server *Server) me(w http.ResponseWriter, request *http.Request) {
	session, csrf, err := server.config.Authenticator.Authenticate(request)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	writePrivateJSON(w, http.StatusOK, struct {
		TelegramUserID int64  `json:"telegram_user_id"`
		Language       string `json:"language"`
		CSRF           string `json:"csrf_token"`
	}{session.TelegramUserID, session.Language, csrf})
}

type languagePreferenceRequest struct {
	Language string `json:"language"`
}

func (server *Server) languagePreference(w http.ResponseWriter, request *http.Request) {
	var payload languagePreferenceRequest
	if err := decodeStrictJSON(w, request, &payload); err != nil ||
		(payload.Language != "ru" && payload.Language != "en") {
		writeProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := server.config.Authenticator.UpdateLanguage(request, payload.Language); err != nil {
		if errors.Is(err, ErrUnauthenticated) || errors.Is(err, errInvalidCSRFRequest) {
			writeProblem(w, http.StatusForbidden, "invalid_request")
			return
		}
		server.logError("persist web language preference", err)
		writeProblem(w, http.StatusServiceUnavailable, "preference_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) logout(w http.ResponseWriter, request *http.Request) {
	if err := server.config.Authenticator.Logout(w, request); err != nil {
		writeProblem(w, http.StatusForbidden, "invalid_request")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) points(w http.ResponseWriter, request *http.Request) {
	session, _, err := server.config.Authenticator.Authenticate(request)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	points, err := server.config.Points.WebPoints(request.Context(), session.TelegramUserID)
	if err != nil {
		server.logError("list saved points", err)
		writeProblem(w, http.StatusServiceUnavailable, "points_unavailable")
		return
	}
	writePrivateJSON(w, http.StatusOK, map[string]any{"points": points})
}

func (server *Server) availability(w http.ResponseWriter, request *http.Request) {
	session, _, err := server.config.Authenticator.Authenticate(request)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if !server.astrodomeAllowed(session.TelegramUserID) {
		writeProblem(w, http.StatusForbidden, "astrodome_disabled")
		return
	}
	availability, err := server.config.Gateway.Availability(request.Context(), session.TelegramUserID)
	if err != nil {
		server.logError("read astrodome availability", err)
		writeProblem(w, http.StatusServiceUnavailable, "astrodome_unavailable")
		return
	}
	writePrivateJSON(w, http.StatusOK, availability)
}

type admissionRequest struct {
	PointID     *int64 `json:"point_id"`
	Coordinates *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"coordinates"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (server *Server) admit(w http.ResponseWriter, request *http.Request) {
	session, err := server.config.Authenticator.RequireCSRF(request)
	if err != nil {
		writeProblem(w, http.StatusForbidden, "invalid_request")
		return
	}
	if !server.astrodomeAllowed(session.TelegramUserID) {
		writeProblem(w, http.StatusForbidden, "astrodome_disabled")
		return
	}
	var payload admissionRequest
	if err := decodeStrictJSON(w, request, &payload); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if (payload.PointID == nil) == (payload.Coordinates == nil) || len(payload.IdempotencyKey) < 16 || len(payload.IdempotencyKey) > 128 {
		writeProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var point SavedPoint
	if payload.PointID != nil {
		point, err = server.config.Points.WebPoint(request.Context(), session.TelegramUserID, *payload.PointID)
		if err != nil {
			http.NotFound(w, request)
			return
		}
	} else {
		point = SavedPoint{Name: "", Latitude: payload.Coordinates.Latitude, Longitude: payload.Coordinates.Longitude}
		if !validCoordinates(point.Latitude, point.Longitude) {
			writeProblem(w, http.StatusBadRequest, "invalid_coordinates")
			return
		}
	}
	status, err := server.config.Gateway.Admit(request.Context(), AstrodomeAdmission{
		TelegramUserID: session.TelegramUserID, Point: point, Language: session.Language,
		IdempotencyKey: payload.IdempotencyKey,
	})
	switch {
	case errors.Is(err, ErrDirectionalBusy):
		writeProblem(w, http.StatusTooManyRequests, "queue_full")
	case errors.Is(err, ErrAstrodomeDisabled), errors.Is(err, ErrAstrodomeUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "astrodome_unavailable")
	case err != nil:
		server.logError("admit astrodome job", err)
		writeProblem(w, http.StatusServiceUnavailable, "astrodome_unavailable")
	default:
		if err := server.config.Visualizations.Begin(request.Context(), session.TelegramUserID, status.ID, point); err != nil {
			_ = server.config.Gateway.Cancel(request.Context(), session.TelegramUserID, status.ID)
			server.logError("record astrodome visualization admission", err)
			writeProblem(w, http.StatusServiceUnavailable, "visualizations_unavailable")
			return
		}
		writePrivateJSON(w, http.StatusAccepted, status)
	}
}

func (server *Server) status(w http.ResponseWriter, request *http.Request) {
	session, _, err := server.config.Authenticator.Authenticate(request)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if !server.astrodomeAllowed(session.TelegramUserID) {
		writeProblem(w, http.StatusForbidden, "astrodome_disabled")
		return
	}
	jobID := request.PathValue("jobID")
	if validateJobID(jobID) != nil {
		http.NotFound(w, request)
		return
	}
	status, err := server.config.Gateway.Status(request.Context(), session.TelegramUserID, jobID)
	if errors.Is(err, ErrJobNotFound) {
		http.NotFound(w, request)
		return
	}
	if err != nil {
		server.logError("read astrodome job", err)
		writeProblem(w, http.StatusServiceUnavailable, "astrodome_unavailable")
		return
	}
	if err := server.config.Visualizations.Reconcile(request.Context(), session.TelegramUserID, status); err != nil {
		server.logError("archive completed astrodome visualization", err)
	}
	writePrivateJSON(w, http.StatusOK, status)
}

func (server *Server) cancel(w http.ResponseWriter, request *http.Request) {
	session, err := server.config.Authenticator.RequireCSRF(request)
	if err != nil {
		writeProblem(w, http.StatusForbidden, "invalid_request")
		return
	}
	if !server.astrodomeAllowed(session.TelegramUserID) {
		writeProblem(w, http.StatusForbidden, "astrodome_disabled")
		return
	}
	jobID := request.PathValue("jobID")
	if validateJobID(jobID) != nil {
		http.NotFound(w, request)
		return
	}
	err = server.config.Gateway.Cancel(request.Context(), session.TelegramUserID, jobID)
	if errors.Is(err, ErrJobNotFound) {
		http.NotFound(w, request)
		return
	}
	if err != nil {
		server.logError("cancel astrodome job", err)
		writeProblem(w, http.StatusServiceUnavailable, "astrodome_unavailable")
		return
	}
	if err := server.config.Visualizations.Reconcile(request.Context(), session.TelegramUserID, AstrodomeJobStatus{ID: jobID, State: "cancelled"}); err != nil {
		server.logError("discard cancelled astrodome visualization admission", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) dataset(w http.ResponseWriter, request *http.Request) {
	session, _, err := server.config.Authenticator.Authenticate(request)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if !server.astrodomeAllowed(session.TelegramUserID) {
		writeProblem(w, http.StatusForbidden, "astrodome_disabled")
		return
	}
	jobID := request.PathValue("jobID")
	if validateJobID(jobID) != nil {
		http.NotFound(w, request)
		return
	}
	status, statusErr := server.config.Gateway.Status(request.Context(), session.TelegramUserID, jobID)
	if statusErr == nil && status.State == "ready" {
		if archiveErr := server.config.Visualizations.Reconcile(request.Context(), session.TelegramUserID, status); archiveErr != nil {
			server.logError("archive astrodome visualization before delivery", archiveErr)
		}
	}
	if archived, archiveErr := server.config.Visualizations.OpenByJob(
		request.Context(), session.TelegramUserID, jobID, server.isAstrodomeAdmin(session.TelegramUserID),
	); archiveErr == nil {
		server.writeVisualizationDataset(w, archived)
		return
	}
	dataset, err := server.config.Gateway.Dataset(request.Context(), session.TelegramUserID, jobID)
	if errors.Is(err, ErrJobNotFound) {
		http.NotFound(w, request)
		return
	}
	if err != nil {
		server.logError("open astrodome dataset", err)
		writeProblem(w, http.StatusServiceUnavailable, "dataset_unavailable")
		return
	}
	defer func() { _ = dataset.Body.Close() }()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	if dataset.ContentEncoding != "" {
		w.Header().Set("Content-Encoding", dataset.ContentEncoding)
	}
	if dataset.ETag != "" {
		w.Header().Set("ETag", dataset.ETag)
	}
	if dataset.Bytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(dataset.Bytes, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, dataset.Body)
}

func (server *Server) visualizations(w http.ResponseWriter, request *http.Request) {
	userID, includeFixture, err := server.visualizationAccess(request)
	if errors.Is(err, ErrAstrodomeDisabled) {
		writeProblem(w, http.StatusForbidden, "astrodome_disabled")
		return
	}
	if err != nil {
		server.logError("authenticate astrodome visualizations", err)
		writeProblem(w, http.StatusServiceUnavailable, "visualizations_unavailable")
		return
	}
	items, err := server.config.Visualizations.List(request.Context(), userID, includeFixture)
	if err != nil {
		server.logError("list astrodome visualizations", err)
		writeProblem(w, http.StatusServiceUnavailable, "visualizations_unavailable")
		return
	}
	writePrivateJSON(w, http.StatusOK, map[string]any{"visualizations": items})
}

func (server *Server) visualizationDataset(w http.ResponseWriter, request *http.Request) {
	userID, includeFixture, err := server.visualizationAccess(request)
	if errors.Is(err, ErrAstrodomeDisabled) {
		writeProblem(w, http.StatusForbidden, "astrodome_disabled")
		return
	}
	if err != nil {
		server.logError("authenticate astrodome visualization", err)
		writeProblem(w, http.StatusServiceUnavailable, "dataset_unavailable")
		return
	}
	dataset, err := server.config.Visualizations.Open(request.Context(), userID, request.PathValue("visualizationID"), includeFixture)
	if errors.Is(err, ErrVisualizationNotFound) {
		http.NotFound(w, request)
		return
	}
	if err != nil {
		server.logError("open astrodome visualization", err)
		writeProblem(w, http.StatusServiceUnavailable, "dataset_unavailable")
		return
	}
	server.writeVisualizationDataset(w, dataset)
}

func (server *Server) writeVisualizationDataset(w http.ResponseWriter, dataset VisualizationDataset) {
	defer func() { _ = dataset.Body.Close() }()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", dataset.ETag)
	w.Header().Set("Content-Length", strconv.FormatInt(dataset.Bytes, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, dataset.Body)
}

func (server *Server) validEdge(value string) bool {
	return subtle.ConstantTimeCompare([]byte(value), server.config.EdgeCredential) == 1
}

func (server *Server) visualizationAccess(request *http.Request) (userID int64, includeFixture bool, err error) {
	session, _, err := server.config.Authenticator.Authenticate(request)
	if errors.Is(err, ErrUnauthenticated) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !server.astrodomeAllowed(session.TelegramUserID) {
		return 0, false, ErrAstrodomeDisabled
	}
	return session.TelegramUserID, server.isAstrodomeAdmin(session.TelegramUserID), nil
}

func (server *Server) astrodomeAllowed(userID int64) bool {
	return server.config.AstrodomePublic || server.isAstrodomeAdmin(userID)
}

func (server *Server) isAstrodomeAdmin(userID int64) bool {
	for _, adminID := range server.config.AstrodomeAdminIDs {
		if userID == adminID {
			return true
		}
	}
	return false
}

func (server *Server) logError(operation string, err error) {
	server.config.Logger.Error(operation, "error", err.Error())
}

func validCoordinates(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsInf(latitude, 0) && !math.IsNaN(longitude) && !math.IsInf(longitude, 0) &&
		latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}

func decodeStrictJSON(w http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(w, request.Body, maxAPIRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request must contain exactly one JSON value")
	}
	return nil
}

func writePrivateJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, status, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	writePrivateJSON(w, status, map[string]string{"error": code})
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
}

func validateJobID(value string) error {
	if len(value) < 20 || len(value) > 128 || strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("invalid job ID")
	}
	return nil
}
