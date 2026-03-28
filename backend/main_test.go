package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func newTestApp(t *testing.T) *app {
	t.Helper()

	seed, err := loadSeed("seed.json")
	if err != nil {
		t.Fatalf("load seed: %v", err)
	}

	application := &app{
		seed:           seed,
		stateFile:      filepath.Join(t.TempDir(), "state.json"),
		jwtSecret:      []byte("test-jwt-secret"),
		allowedOrigins: parseAllowedOrigins("http://localhost:3001,http://localhost:5173"),
		clients:        make(map[*client]struct{}),
		loginLimiter:   newRateLimiter(),
		actionLimiter:  newRateLimiter(),
		wsLimiter:      newRateLimiter(),
	}
	application.upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return application.isAllowedRequestOrigin(r) },
	}
	application.state = application.seedState()
	application.normalizeState()

	return application
}

func newTestHandler(a *app) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", a.handleHealth)
	mux.HandleFunc("/api/openapi.yaml", a.handleOpenAPISpec)
	mux.HandleFunc("/api/docs", a.handleAPIDocs)
	mux.HandleFunc("/api/auth/login", a.handleLogin)
	mux.HandleFunc("/api/bootstrap", a.withAuth(a.handleBootstrap))
	mux.HandleFunc("/api/actions/", a.withAuth(a.handleAction))
	return a.withCORS(mux)
}

func performJSONRequest(t *testing.T, handler http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func performJSONRequestWithOrigin(t *testing.T, handler http.Handler, method, path string, body any, token, origin string) *httptest.ResponseRecorder {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeJSONResponse[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(rr.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rr.Body.String())
	}
	return value
}

func loginTeacher(t *testing.T, handler http.Handler, login, password string) LoginResponse {
	t.Helper()

	rr := performJSONRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{
		"mode":     "teacher",
		"login":    login,
		"password": password,
	}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	return decodeJSONResponse[LoginResponse](t, rr)
}

func loginStudent(t *testing.T, handler http.Handler, surname string) LoginResponse {
	t.Helper()

	rr := performJSONRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{
		"mode":    "student",
		"surname": surname,
	}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	return decodeJSONResponse[LoginResponse](t, rr)
}

func TestTeacherLoginUpgradesLegacyPasswordHash(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	response := loginTeacher(t, handler, "admin", "adminmephi")
	if response.Token == "" {
		t.Fatal("expected JWT token in login response")
	}
	if !response.Runtime.IsAuthenticated {
		t.Fatal("expected authenticated runtime after login")
	}
	if len(response.Catalog.Users) < 2 {
		t.Fatalf("expected full teacher catalog, got %d users", len(response.Catalog.Users))
	}

	application.mu.RLock()
	admin, ok := application.findUserByID("admin-1")
	application.mu.RUnlock()
	if !ok {
		t.Fatal("admin user not found after login")
	}
	if !strings.HasPrefix(admin.PasswordHash, "argon2id$") {
		t.Fatalf("expected upgraded argon2id hash, got %q", admin.PasswordHash)
	}
}

func TestStudentLoginReturnsSanitizedCatalog(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	response := loginStudent(t, handler, "Fadeev")
	if len(response.Catalog.Users) != 1 {
		t.Fatalf("expected only current student in catalog, got %d users", len(response.Catalog.Users))
	}
	if response.Catalog.Users[0].PasswordHash != "" {
		t.Fatal("student catalog leaked password hash")
	}
	if len(response.Catalog.Seminars) == 0 {
		t.Fatal("expected at least one accessible live seminar for student")
	}
	for _, seminar := range response.Catalog.Seminars {
		if seminar.Status != SeminarLive {
			t.Fatalf("student received non-live seminar %s with status %s", seminar.ID, seminar.Status)
		}
		if seminar.AccessCode != "" {
			t.Fatalf("student received access code for seminar %s", seminar.ID)
		}
	}
	if len(response.Catalog.Tasks) == 0 {
		t.Fatal("expected seminar tasks in student catalog")
	}
	for _, task := range response.Catalog.Tasks {
		if task.ExpectedQuery != "" {
			t.Fatalf("student received expected query for task %s", task.ID)
		}
		if task.StarterSQL != "" {
			t.Fatalf("student received starter SQL for task %s", task.ID)
		}
	}
	for _, challenge := range response.Catalog.PlaygroundChallenges {
		if challenge.ExpectedQuery != "" || challenge.StarterSQL != "" {
			t.Fatalf("student received hidden playground solution for challenge %s", challenge.ID)
		}
	}
}

func TestBootstrapRestoresSessionForAuthenticatedUser(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	loginResponse := loginTeacher(t, handler, "admin", "adminmephi")

	bootstrapRR := performJSONRequest(t, handler, http.MethodGet, "/api/bootstrap", nil, loginResponse.Token)
	if bootstrapRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", bootstrapRR.Code, bootstrapRR.Body.String())
	}

	bootstrapResponse := decodeJSONResponse[RuntimeResponse](t, bootstrapRR)
	if bootstrapResponse.Runtime.SessionID != loginResponse.Runtime.SessionID {
		t.Fatalf("expected session %q, got %q", loginResponse.Runtime.SessionID, bootstrapResponse.Runtime.SessionID)
	}
	if bootstrapResponse.Runtime.CurrentUserID != "admin-1" {
		t.Fatalf("expected current user admin-1, got %q", bootstrapResponse.Runtime.CurrentUserID)
	}
	if len(bootstrapResponse.Catalog.Seminars) == 0 {
		t.Fatal("expected seminars in bootstrap response")
	}
}

func TestBootstrapRejectsInvalidToken(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	rr := performJSONRequest(t, handler, http.MethodGet, "/api/bootstrap", nil, "invalid-token")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Invalid token") {
		t.Fatalf("expected invalid token error, got: %s", rr.Body.String())
	}
}

func TestLoginRejectsDisallowedOrigin(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	rr := performJSONRequestWithOrigin(t, handler, http.MethodPost, "/api/auth/login", map[string]string{
		"mode":     "teacher",
		"login":    "admin",
		"password": "adminmephi",
	}, "", "https://evil.example.com")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Origin запрещён") {
		t.Fatalf("expected origin error, got: %s", rr.Body.String())
	}
}

func TestStudentCannotOpenClosedSeminar(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	loginResponse := loginStudent(t, handler, "Fadeev")
	rr := performJSONRequest(t, handler, http.MethodPost, "/api/actions/select-seminar", map[string]string{
		"seminarId": "seminar-joins-archive",
	}, loginResponse.Token)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "только активный семинар") {
		t.Fatalf("expected closed seminar error, got: %s", rr.Body.String())
	}
}

func TestStudentCannotExecuteTeacherOnlyAction(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	loginResponse := loginStudent(t, handler, "Fadeev")
	rr := performJSONRequest(t, handler, http.MethodPost, "/api/actions/create-user", map[string]string{
		"role":     "teacher",
		"fullName": "Test Teacher",
		"login":    "ttest",
		"password": "secret123",
	}, loginResponse.Token)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Недостаточно прав") {
		t.Fatalf("expected role error, got: %s", rr.Body.String())
	}
}

func TestTeacherCanRevealReferenceSolutionToStudents(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	teacherLogin := loginTeacher(t, handler, "admin", "adminmephi")
	selectRR := performJSONRequest(t, handler, http.MethodPost, "/api/actions/select-seminar", map[string]string{
		"seminarId": "seminar-sql-bootcamp",
	}, teacherLogin.Token)
	if selectRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on seminar select, got %d: %s", selectRR.Code, selectRR.Body.String())
	}

	toggleRR := performJSONRequest(t, handler, http.MethodPost, "/api/actions/toggle-setting", map[string]string{
		"setting": "referenceSolutionVisible",
	}, teacherLogin.Token)
	if toggleRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on toggle, got %d: %s", toggleRR.Code, toggleRR.Body.String())
	}

	studentLogin := loginStudent(t, handler, "Fadeev")
	foundOpenReference := false
	for _, task := range studentLogin.Catalog.Tasks {
		if task.SeminarID != "seminar-sql-bootcamp" {
			continue
		}
		if task.ExpectedQuery != "" || task.StarterSQL != "" {
			foundOpenReference = true
			break
		}
	}
	if !foundOpenReference {
		t.Fatal("expected at least one task with visible reference or starter SQL after teacher toggle")
	}
}

func TestDocumentationEndpointsAreServed(t *testing.T) {
	application := newTestApp(t)
	handler := newTestHandler(application)

	openapiRR := performJSONRequest(t, handler, http.MethodGet, "/api/openapi.yaml", nil, "")
	if openapiRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for openapi, got %d: %s", openapiRR.Code, openapiRR.Body.String())
	}
	if !strings.Contains(openapiRR.Body.String(), "openapi: 3.1.0") {
		t.Fatalf("expected openapi content, got: %s", openapiRR.Body.String())
	}

	docsRR := performJSONRequest(t, handler, http.MethodGet, "/api/docs", nil, "")
	if docsRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for docs, got %d: %s", docsRR.Code, docsRR.Body.String())
	}
	if !strings.Contains(docsRR.Body.String(), "IVT Playground API") {
		t.Fatalf("expected docs HTML content, got: %s", docsRR.Body.String())
	}
}
