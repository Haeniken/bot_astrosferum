package directional

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bot_astrosferum/internal/forecast"
)

const (
	ServiceCredentialHeader       = "X-Astrosferum-Service"
	UserIDHeader                  = "X-Astrosferum-User-ID"
	minimumServiceCredentialBytes = 32
	maximumInternalJSONBytes      = 16 << 10
	workerHealthTimeout           = 2 * time.Second
)

type HTTPConfig struct {
	Coordinator       *Coordinator
	AstrodomeBackend  AstrodomeBackend
	WorkerHealth      WorkerHealth
	AccountJobs       AccountJobBackend
	ServiceCredential []byte
}

type HTTPHandler struct {
	coordinator      *Coordinator
	astrodomeBackend AstrodomeBackend
	workerHealth     WorkerHealth
	accountJobs      AccountJobBackend
	credentialDigest [sha256.Size]byte
	mux              *http.ServeMux
}

type AstrodomeJobStatus struct {
	ID              string     `json:"id"`
	State           string     `json:"state"`
	QueuePosition   *int       `json:"queue_position"`
	EstimatedAt     *time.Time `json:"estimated_at,omitempty"`
	ProgressPercent float64    `json:"progress_percent"`
	EstimateBasis   string     `json:"estimate_basis,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Provider        string     `json:"provider,omitempty"`
	RunID           string     `json:"run_id,omitempty"`
	GridProfile     string     `json:"grid_profile,omitempty"`
	GeometryDigest  string     `json:"grid_geometry_digest,omitempty"`
	DatasetBytes    int64      `json:"dataset_bytes,omitempty"`
	FailureCode     string     `json:"failure_code,omitempty"`
}

func NewHTTPHandler(config HTTPConfig) (*HTTPHandler, error) {
	if config.Coordinator == nil || config.AstrodomeBackend == nil || config.WorkerHealth == nil {
		return nil, errors.New("directional coordinator, Astrodome backend, and worker health probe are required")
	}
	if len(config.ServiceCredential) < minimumServiceCredentialBytes {
		return nil, fmt.Errorf("directional service credential must contain at least %d bytes", minimumServiceCredentialBytes)
	}
	handler := &HTTPHandler{
		coordinator: config.Coordinator, astrodomeBackend: config.AstrodomeBackend, workerHealth: config.WorkerHealth,
		accountJobs:      config.AccountJobs,
		credentialDigest: sha256.Sum256(config.ServiceCredential), mux: http.NewServeMux(),
	}
	handler.mux.HandleFunc("GET /internal/v1/directional/astrodome/availability", handler.availability)
	handler.mux.HandleFunc("POST /internal/v1/directional/astrodome/jobs", handler.submitAstrodome)
	handler.mux.HandleFunc("GET /internal/v1/directional/astrodome/jobs", handler.astrodomeJobs)
	handler.mux.HandleFunc("GET /internal/v1/directional/astrodome/jobs/current", handler.currentStatus)
	handler.mux.HandleFunc("GET /internal/v1/directional/astrodome/jobs/{jobID}", handler.status)
	handler.mux.HandleFunc("DELETE /internal/v1/directional/astrodome/jobs/{jobID}", handler.cancel)
	handler.mux.HandleFunc("GET /internal/v1/directional/astrodome/jobs/{jobID}/dataset", handler.dataset)
	if handler.accountJobs != nil {
		handler.mux.HandleFunc("POST /internal/v1/account/jobs/{kind}", handler.submitAccountJob)
		handler.mux.HandleFunc("GET /internal/v1/account/jobs", handler.accountJobsList)
		handler.mux.HandleFunc("GET /internal/v1/account/jobs/{jobID}", handler.accountJobStatus)
		handler.mux.HandleFunc("DELETE /internal/v1/account/jobs/{jobID}", handler.cancelAccountJob)
		handler.mux.HandleFunc("GET /internal/v1/account/jobs/{jobID}/files/{fileName}", handler.accountJobFile)
	}
	return handler, nil
}

func (handler *HTTPHandler) astrodomeJobs(w http.ResponseWriter, request *http.Request) {
	ownerID, _, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	statuses, err := handler.coordinator.Statuses(ownerID, KindAstrodome)
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	jobs := make([]AstrodomeJobStatus, len(statuses))
	for index := range statuses {
		jobs[index] = astrodomeStatus(statuses[index])
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (handler *HTTPHandler) accountJobsList(w http.ResponseWriter, request *http.Request) {
	_, numericUserID, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	kind := AccountJobKind(request.URL.Query().Get("kind"))
	if kind != AccountJobForecast && kind != AccountJobHorizon {
		writeHTTPProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	jobs, err := handler.accountJobs.AccountJobs(request.Context(), numericUserID, kind)
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (handler *HTTPHandler) submitAccountJob(w http.ResponseWriter, request *http.Request) {
	_, numericUserID, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	if contentType := request.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		writeHTTPProblem(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	kind := AccountJobKind(request.PathValue("kind"))
	if kind != AccountJobForecast && kind != AccountJobHorizon {
		http.NotFound(w, request)
		return
	}
	var admission AccountJobAdmission
	if err := decodeInternalJSON(w, request, &admission); err != nil || admission.TelegramUserID != numericUserID || admission.Validate() != nil {
		writeHTTPProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	status, err := handler.accountJobs.AdmitAccountJob(request.Context(), kind, admission)
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusAccepted, status)
}

func (handler *HTTPHandler) accountJobStatus(w http.ResponseWriter, request *http.Request) {
	_, numericUserID, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	status, err := handler.accountJobs.AccountJobStatus(request.Context(), numericUserID, request.PathValue("jobID"))
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, status)
}

func (handler *HTTPHandler) cancelAccountJob(w http.ResponseWriter, request *http.Request) {
	_, numericUserID, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	if err := handler.accountJobs.CancelAccountJob(request.Context(), numericUserID, request.PathValue("jobID")); err != nil {
		writeDirectionalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) accountJobFile(w http.ResponseWriter, request *http.Request) {
	_, numericUserID, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	output, err := handler.accountJobs.OpenAccountJobFile(request.Context(), numericUserID, request.PathValue("jobID"), request.PathValue("fileName"))
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	defer func() { _ = output.Body.Close() }()
	w.Header().Set("Content-Type", output.MediaType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if output.ETag != "" {
		w.Header().Set("ETag", output.ETag)
	}
	if output.Bytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(output.Bytes, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, output.Body)
}

func (handler *HTTPHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	candidate := sha256.Sum256([]byte(request.Header.Get(ServiceCredentialHeader)))
	if subtle.ConstantTimeCompare(candidate[:], handler.credentialDigest[:]) != 1 {
		writeHTTPProblem(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	handler.mux.ServeHTTP(w, request)
}

func (handler *HTTPHandler) availability(w http.ResponseWriter, request *http.Request) {
	_, numericUserID, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	availability, err := handler.astrodomeBackend.Availability(request.Context(), numericUserID)
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	stats := handler.coordinator.Stats()
	availability.QueueLength, availability.Running = stats.QueueLength, stats.Running
	healthContext, cancel := context.WithTimeout(request.Context(), workerHealthTimeout)
	availability.WorkerAvailable = handler.workerHealth.Health(healthContext) == nil
	cancel()
	writeHTTPJSON(w, http.StatusOK, availability)
}

func (handler *HTTPHandler) submitAstrodome(w http.ResponseWriter, request *http.Request) {
	ownerID, numericUserID, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	if contentType := request.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		writeHTTPProblem(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var admission AstrodomeAdmission
	if err := decodeInternalJSON(w, request, &admission); err != nil {
		writeHTTPProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if admission.TelegramUserID != numericUserID || !validAdmission(admission) {
		writeHTTPProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	prepared, err := handler.astrodomeBackend.Prepare(request.Context(), admission)
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	prepared, err = validatePreparedAstrodome(prepared)
	if err != nil {
		writeDirectionalError(w, ErrUnavailable)
		return
	}
	ticket, err := handler.coordinator.Submit(request.Context(), Request{
		Kind: KindAstrodome, OwnerID: ownerID, OwnerActiveLimit: prepared.OwnerActiveLimit, IdempotencyKey: admission.IdempotencyKey,
		RequestFamilyKey: prepared.RequestFamilyKey, ScienceCacheKey: prepared.ScienceCacheKey,
		Source: prepared.Source, Payload: prepared.Payload,
	})
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	status, err := ticket.Status()
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusAccepted, astrodomeStatus(status))
}

func (handler *HTTPHandler) status(w http.ResponseWriter, request *http.Request) {
	ownerID, _, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	jobID := request.PathValue("jobID")
	if !validJobID(jobID) {
		http.NotFound(w, request)
		return
	}
	status, err := handler.coordinator.Status(ownerID, jobID)
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, astrodomeStatus(status))
}

func (handler *HTTPHandler) currentStatus(w http.ResponseWriter, request *http.Request) {
	ownerID, _, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	status, err := handler.coordinator.LatestStatus(ownerID, KindAstrodome)
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, astrodomeStatus(status))
}

func (handler *HTTPHandler) cancel(w http.ResponseWriter, request *http.Request) {
	ownerID, _, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	jobID := request.PathValue("jobID")
	if !validJobID(jobID) {
		http.NotFound(w, request)
		return
	}
	if err := handler.coordinator.Cancel(ownerID, jobID); err != nil {
		writeDirectionalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) dataset(w http.ResponseWriter, request *http.Request) {
	ownerID, _, ok := internalOwner(request)
	if !ok {
		writeHTTPProblem(w, http.StatusUnauthorized, "user_required")
		return
	}
	jobID := request.PathValue("jobID")
	if !validJobID(jobID) {
		http.NotFound(w, request)
		return
	}
	file, result, err := handler.coordinator.OpenResult(ownerID, jobID)
	if err != nil {
		writeDirectionalError(w, err)
		return
	}
	defer func() { _ = file.Close() }()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", result.ETag)
	if result.ContentEncoding != "" {
		w.Header().Set("Content-Encoding", result.ContentEncoding)
	}
	if result.Bytes >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(result.Bytes, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func internalOwner(request *http.Request) (string, int64, bool) {
	value := request.Header.Get(UserIDHeader)
	userID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || userID <= 0 || strconv.FormatInt(userID, 10) != value {
		return "", 0, false
	}
	return "telegram:" + value, userID, true
}

func validAdmission(admission AstrodomeAdmission) bool {
	if len(admission.IdempotencyKey) < 16 || len(admission.IdempotencyKey) > 128 {
		return false
	}
	if admission.Language != "ru" && admission.Language != "en" {
		return false
	}
	if len(admission.Point.Name) > 512 {
		return false
	}
	return finiteBetween(admission.Point.Latitude, -90, 90) && finiteBetween(admission.Point.Longitude, -180, 180)
}

func finiteBetween(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

func validJobID(value string) bool {
	if len(value) < 20 || len(value) > 128 || !strings.HasPrefix(value, "job_") {
		return false
	}
	for _, character := range value[4:] {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func astrodomeStatus(status JobStatus) AstrodomeJobStatus {
	return AstrodomeJobStatus{
		ID: status.ID, State: string(status.State), QueuePosition: status.QueuePosition,
		EstimatedAt: status.EstimatedAt, ProgressPercent: status.ProgressPercent, EstimateBasis: status.EstimateBasis,
		CreatedAt: status.CreatedAt, UpdatedAt: status.UpdatedAt,
		Provider: status.Provider, RunID: status.RunID, GridProfile: status.GridProfile,
		GeometryDigest: status.GeometryDigest, DatasetBytes: status.DatasetBytes,
		FailureCode: status.FailureCode,
	}
}

func decodeInternalJSON(w http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(w, request.Body, maximumInternalJSONBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("internal request must contain exactly one JSON value")
	}
	return nil
}

func writeDirectionalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeHTTPProblem(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrQueueFull):
		writeHTTPProblem(w, http.StatusTooManyRequests, "queue_full")
	case errors.Is(err, ErrOwnerBusy):
		writeHTTPProblem(w, http.StatusTooManyRequests, "owner_busy")
	case errors.Is(err, ErrDisabled):
		writeHTTPProblem(w, http.StatusForbidden, "astrodome_disabled")
	case errors.Is(err, ErrAccountUnavailable):
		writeHTTPProblem(w, http.StatusServiceUnavailable, "account_job_unavailable")
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrNotStarted), errors.Is(err, ErrClosed):
		writeHTTPProblem(w, http.StatusServiceUnavailable, "astrodome_unavailable")
	case errors.Is(err, ErrIdempotencyConflict):
		writeHTTPProblem(w, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeHTTPProblem(w, http.StatusServiceUnavailable, "request_cancelled")
	default:
		writeHTTPProblem(w, http.StatusServiceUnavailable, "astrodome_unavailable")
	}
}

func validatePreparedAstrodome(prepared PreparedAstrodome) (PreparedAstrodome, error) {
	if prepared.RequestFamilyKey == "" {
		prepared.RequestFamilyKey = prepared.ScienceCacheKey
	}
	_, gridProfileErr := forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridProfileID(prepared.Source.GridProfile))
	if (prepared.OwnerActiveLimit != 0 && prepared.OwnerActiveLimit != 1) ||
		prepared.RequestFamilyKey == "" || len(prepared.RequestFamilyKey) > 4096 ||
		prepared.ScienceCacheKey == "" || len(prepared.ScienceCacheKey) > 4096 ||
		prepared.Source.Provider != "icon-eu" ||
		!validICONRunID(prepared.Source.RunID) ||
		gridProfileErr != nil ||
		!validPrefixedSHA256(prepared.Source.GeometryDigest) {
		return PreparedAstrodome{}, errors.New("prepared Astrodome source is invalid")
	}
	prepared.Source.GeometryDigest = strings.ToLower(prepared.Source.GeometryDigest)
	return prepared, nil
}

func validICONRunID(value string) bool {
	if len(value) != 10 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') && (character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func writeHTTPJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeHTTPProblem(w http.ResponseWriter, status int, code string) {
	writeHTTPJSON(w, status, map[string]string{"error": code})
}
