package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

type contextKey string

const (
	contextUserID    contextKey = "userId"
	contextSessionID contextKey = "sessionId"
)

type client struct {
	conn      *websocket.Conn
	userID    string
	sessionID string
	mu        sync.Mutex
}

type app struct {
	seed       SeedData
	state      ServerState
	stateFile  string
	jwtSecret  []byte
	allowedOrigins map[string]struct{}
	mu         sync.RWMutex
	clients    map[*client]struct{}
	clientsMu  sync.RWMutex
	loginLimiter  *rateLimiter
	actionLimiter *rateLimiter
	wsLimiter     *rateLimiter
	upgrader   websocket.Upgrader
	httpServer *http.Server
}

func main() {
	seed, err := loadSeed("seed.json")
	if err != nil {
		log.Fatalf("load seed: %v", err)
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Print("WARNING: JWT_SECRET не задан, используется небезопасный dev-secret. Для сервера задайте собственный секрет.")
		jwtSecret = "sql-seminar-platform-dev-secret"
	}

	stateFile := filepath.Join("..", "server-data", "state.json")
	application := &app{
		seed:           seed,
		stateFile:      stateFile,
		jwtSecret:      []byte(jwtSecret),
		allowedOrigins: parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS")),
		clients:        make(map[*client]struct{}),
		loginLimiter:   newRateLimiter(),
		actionLimiter:  newRateLimiter(),
		wsLimiter:      newRateLimiter(),
	}
	application.upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return application.isAllowedRequestOrigin(r) },
	}

	if err := application.loadOrCreateState(); err != nil {
		log.Fatalf("state init: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", application.handleHealth)
	mux.HandleFunc("/api/auth/login", application.handleLogin)
	mux.HandleFunc("/api/bootstrap", application.withAuth(application.handleBootstrap))
	mux.HandleFunc("/api/reset", application.withAuth(application.handleReset))
	mux.HandleFunc("/api/actions/", application.withAuth(application.handleAction))
	mux.HandleFunc("/ws", application.handleWS)
	mux.HandleFunc("/", application.handleStatic)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}

	application.httpServer = &http.Server{
		Addr:    ":" + port,
		Handler: application.withCORS(mux),
	}

	log.Printf("Go SQL Seminar backend listening on http://localhost:%s", port)
	if err := application.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func loadSeed(path string) (SeedData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SeedData{}, err
	}
	var seed SeedData
	if err := json.Unmarshal(data, &seed); err != nil {
		return SeedData{}, err
	}
	return seed, nil
}

func (a *app) seedState() ServerState {
	runtime := cloneRuntime(a.seed.Runtime)
	runtime.IsAuthenticated = false
	runtime.CurrentUserID = ""
	runtime.SessionID = ""
	if runtime.SelectedSeminarByUser == nil {
		runtime.SelectedSeminarByUser = map[string]string{}
	}
	return ServerState{
		Runtime:                  runtime,
		UserPlaygroundSelections: map[string]PlaygroundSelection{},
	}
}

func cloneRuntime(runtime PlatformRuntime) PlatformRuntime {
	data, _ := json.Marshal(runtime)
	var cloned PlatformRuntime
	_ = json.Unmarshal(data, &cloned)
	return cloned
}

func cloneState(state ServerState) ServerState {
	data, _ := json.Marshal(state)
	var cloned ServerState
	_ = json.Unmarshal(data, &cloned)
	return cloned
}

func (a *app) loadOrCreateState() error {
	if err := os.MkdirAll(filepath.Dir(a.stateFile), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(a.stateFile); errors.Is(err, os.ErrNotExist) {
		a.state = a.seedState()
		return a.saveStateLocked()
	}
	data, err := os.ReadFile(a.stateFile)
	if err != nil {
		return err
	}
	var state ServerState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	a.state = state
	a.normalizeState()
	return nil
}

func (a *app) normalizeState() {
	if a.state.Runtime.CreatedUsers == nil {
		a.state.Runtime.CreatedUsers = []User{}
	}
	if a.state.Runtime.CreatedGroups == nil {
		a.state.Runtime.CreatedGroups = []Group{}
	}
	if a.state.Runtime.UserOverrides == nil {
		a.state.Runtime.UserOverrides = map[string]User{}
	}
	if a.state.Runtime.GroupOverrides == nil {
		a.state.Runtime.GroupOverrides = map[string]Group{}
	}
	if a.state.Runtime.DeletedUserIDs == nil {
		a.state.Runtime.DeletedUserIDs = []string{}
	}
	if a.state.Runtime.DeletedGroupIDs == nil {
		a.state.Runtime.DeletedGroupIDs = []string{}
	}
	if a.state.Runtime.SeminarMeta == nil {
		a.state.Runtime.SeminarMeta = map[string]SeminarMetaOverride{}
	}
	if a.state.Runtime.SeminarStudentIDs == nil {
		a.state.Runtime.SeminarStudentIDs = map[string][]string{}
	}
	if a.state.Runtime.SeminarTaskIDs == nil {
		a.state.Runtime.SeminarTaskIDs = map[string][]string{}
	}
	if a.state.Runtime.SelectedSeminarByUser == nil {
		a.state.Runtime.SelectedSeminarByUser = map[string]string{}
	}
	if a.state.Runtime.SelectedTaskByUser == nil {
		a.state.Runtime.SelectedTaskByUser = map[string]string{}
	}
	if a.state.Runtime.Drafts == nil {
		a.state.Runtime.Drafts = map[string]string{}
	}
	if a.state.Runtime.SeminarRuntime == nil {
		a.state.Runtime.SeminarRuntime = map[string]SeminarRuntime{}
	}
}

func (a *app) saveStateLocked() error {
	data, err := json.MarshalIndent(a.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.stateFile, data, 0o644)
}

func createID(prefix string) string {
	randomPart, _ := rand.Int(rand.Reader, big.NewInt(1_000_000_000))
	return fmt.Sprintf("%s-%x-%x", prefix, randomPart.Int64(), time.Now().UnixNano())
}

func (a *app) signToken(userID, sessionID string) (string, error) {
	claims := jwt.MapClaims{
		"sub":       userID,
		"userId":    userID,
		"sessionId": sessionID,
		"iat":       time.Now().Unix(),
		"nbf":       time.Now().Unix(),
		"iss":       "ivt-playground",
		"aud":       "ivt-playground-ui",
		"exp":       time.Now().Add(12 * time.Hour).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.jwtSecret)
}

func (a *app) verifyToken(token string) (string, string, error) {
	parser := jwt.NewParser(jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	parsed, err := parser.Parse(token, func(_ *jwt.Token) (any, error) {
		return a.jwtSecret, nil
	})
	if err != nil || !parsed.Valid {
		return "", "", errors.New("invalid token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", errors.New("invalid claims")
	}
	userID, _ := claims["userId"].(string)
	sessionID, _ := claims["sessionId"].(string)
	issuer, _ := claims["iss"].(string)
	if userID == "" || sessionID == "" {
		return "", "", errors.New("missing claims")
	}
	if issuer != "ivt-playground" {
		return "", "", errors.New("invalid issuer")
	}
	return userID, sessionID, nil
}

func (a *app) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.setSecurityHeaders(w)
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !a.isAllowedRequestOrigin(r) {
				writeError(w, http.StatusForbidden, "Origin запрещён.")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *app) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "Missing token")
			return
		}
		userID, sessionID, err := a.verifyToken(strings.TrimPrefix(authHeader, "Bearer "))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), contextUserID, userID)
		ctx = context.WithValue(ctx, contextSessionID, sessionID)
		next(w, r.WithContext(ctx))
	}
}

func userIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(contextUserID).(string)
	return value
}

func sessionIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(contextSessionID).(string)
	return value
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func (a *app) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Метод не поддерживается.")
		return
	}

	ip := clientIP(r)
	if allowed, retryAfter := a.loginLimiter.allow("login:"+ip, rateLimitPolicy{Limit: 8, Window: 15 * time.Minute, Lockout: 30 * time.Minute}); !allowed {
		writeRateLimitError(w, retryAfter)
		return
	}

	var body struct {
		Mode     string `json:"mode"`
		Login    string `json:"login"`
		Password string `json:"password"`
		Surname  string `json:"surname"`
	}
	if err := decodeJSONBody(w, r, &body, maxAuthBodyBytes); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "Тело запроса пустое.")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var (
		user User
		ok   bool
	)

	mode := sanitizeText(body.Mode, 16, false)
	switch mode {
	case "", "teacher":
		login, err := validateLoginValue(body.Login)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		password, err := validatePasswordValue(body.Password)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		user, ok = a.findUserByLogin(login)
		if !ok {
			writeError(w, http.StatusUnauthorized, "Неверные данные входа.")
			return
		}
		if user.Role != RoleTeacher && user.Role != RoleAdmin {
			writeError(w, http.StatusUnauthorized, "Неверные данные входа.")
			return
		}
		passwordValid, passwordNeedsUpgrade := verifyPasswordHash(password, user.PasswordHash)
		if !passwordValid {
			writeError(w, http.StatusUnauthorized, "Неверные данные входа.")
			return
		}
		a.mu.Lock()
		if passwordNeedsUpgrade {
			a.upgradeUserPasswordHashLocked(user.ID, password)
		}
		a.mu.Unlock()
	case "student":
		surname, err := validateSurnameValue(body.Surname)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		user, ok = a.findStudentBySurname(surname)
		if !ok {
			writeError(w, http.StatusUnauthorized, "Неверные данные входа.")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "Неизвестный режим входа.")
		return
	}

	sessionID := createID("session")
	token, err := a.signToken(user.ID, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.mu.Lock()
	a.appendEventLocked(user, sessionID, "auth.login", map[string]any{"login": user.Login, "mode": firstNonEmpty(mode, "teacher"), "ip": ip}, "", "", "", "", "", 0)
	catalog, runtime, err := a.buildResponseForUserLocked(user.ID, sessionID)
	if err != nil {
		a.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "Unknown user")
		return
	}
	if err := a.saveStateLocked(); err != nil {
		a.mu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.mu.Unlock()
	a.loginLimiter.reset("login:" + ip)
	a.broadcastState()

	writeJSON(w, http.StatusOK, LoginResponse{Token: token, Catalog: catalog, Runtime: runtime})
}

func (a *app) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Метод не поддерживается.")
		return
	}
	userID := userIDFromContext(r.Context())
	sessionID := sessionIDFromContext(r.Context())
	if _, ok := a.findUserByID(userID); !ok {
		writeError(w, http.StatusUnauthorized, "Unknown user")
		return
	}

	a.mu.RLock()
	catalog, runtime, err := a.buildResponseForUserLocked(userID, sessionID)
	a.mu.RUnlock()
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Unknown user")
		return
	}
	writeJSON(w, http.StatusOK, RuntimeResponse{Catalog: catalog, Runtime: runtime})
}

func (a *app) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Метод не поддерживается.")
		return
	}
	user, ok := a.findUserByID(userIDFromContext(r.Context()))
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unknown user")
		return
	}
	if user.Role == RoleStudent {
		writeError(w, http.StatusForbidden, "Недостаточно прав.")
		return
	}

	sessionID := sessionIDFromContext(r.Context())
	a.mu.Lock()
	a.state = a.seedState()
	a.appendEventLocked(user, sessionID, "system.reset", map[string]any{}, "", "", "", "", "", 0)
	catalog, runtime, err := a.buildResponseForUserLocked(user.ID, sessionID)
	if err != nil {
		a.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "Unknown user")
		return
	}
	if err := a.saveStateLocked(); err != nil {
		a.mu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.mu.Unlock()
	a.broadcastState()

	writeJSON(w, http.StatusOK, RuntimeResponse{Catalog: catalog, Runtime: runtime})
}

func (a *app) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Метод не поддерживается.")
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/actions/")
	if allowed, retryAfter := a.actionLimiter.allow("action:"+userIDFromContext(r.Context())+":"+action, validateActionRateLimit(action)); !allowed {
		writeRateLimitError(w, retryAfter)
		return
	}
	var payload map[string]any
	if err := decodeJSONBody(w, r, &payload, maxActionBodyBytes); err != nil {
		if errors.Is(err, io.EOF) {
			payload = map[string]any{}
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}

	user, ok := a.findUserByID(userIDFromContext(r.Context()))
	if !ok {
		writeError(w, http.StatusUnauthorized, "Unknown user")
		return
	}
	sessionID := sessionIDFromContext(r.Context())

	runtime, err := a.performAction(action, payload, user, sessionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.mu.RLock()
	catalog, _, catalogErr := a.buildResponseForUserLocked(user.ID, sessionID)
	a.mu.RUnlock()
	if catalogErr != nil {
		writeError(w, http.StatusUnauthorized, "Unknown user")
		return
	}
	writeJSON(w, http.StatusOK, RuntimeResponse{Catalog: catalog, Runtime: runtime})
}

func (a *app) performAction(action string, payload map[string]any, user User, sessionID string) (PlatformRuntime, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	seminar := a.selectedSeminarForUserLocked(user)
	var err error

	updatePlaygroundSelection := func(partial PlaygroundSelection) {
		current := a.state.UserPlaygroundSelections[user.ID]
		if current.SelectedPlaygroundTemplateID == "" {
			current.SelectedPlaygroundTemplateID = a.state.Runtime.SelectedPlaygroundTemplateID
			current.SelectedPlaygroundChallengeID = a.state.Runtime.SelectedPlaygroundChallengeID
			current.SelectedPlaygroundDatasetID = a.state.Runtime.SelectedPlaygroundDatasetID
		}
		if partial.SelectedPlaygroundTemplateID != "" {
			current.SelectedPlaygroundTemplateID = partial.SelectedPlaygroundTemplateID
		}
		if partial.SelectedPlaygroundChallengeID != "" {
			current.SelectedPlaygroundChallengeID = partial.SelectedPlaygroundChallengeID
		}
		if partial.SelectedPlaygroundDatasetID != "" {
			current.SelectedPlaygroundDatasetID = partial.SelectedPlaygroundDatasetID
		}
		a.state.UserPlaygroundSelections[user.ID] = current
	}

	switch action {
	case "save-draft":
		key := sanitizeText(payloadString(payload, "key"), 120, false)
		if !strings.HasPrefix(key, user.ID+":") {
			return PlatformRuntime{}, errors.New("Нельзя сохранять черновик в чужой области.")
		}
		value, err := validateSQLValue("Черновик", payloadString(payload, "value"), maxDraftLength, true)
		if err != nil {
			return PlatformRuntime{}, err
		}
		a.state.Runtime.Drafts[key] = value
	case "select-seminar":
		seminarID := sanitizeText(payloadString(payload, "seminarId"), 80, false)
		nextSeminar, ok := a.findSeminarLocked(seminarID)
		if !ok {
			return PlatformRuntime{}, errors.New("Семинар не найден.")
		}
		if user.Role == RoleStudent && nextSeminar.Status != SeminarLive {
			return PlatformRuntime{}, errors.New("Студент может открыть только активный семинар.")
		}
		if !a.canAccessSeminarLocked(user, nextSeminar) {
			return PlatformRuntime{}, errors.New("Нет доступа к этому семинару.")
		}
		a.state.Runtime.SelectedSeminarByUser[user.ID] = nextSeminar.ID
		if len(nextSeminar.TaskIDs) > 0 && !contains(nextSeminar.TaskIDs, a.state.Runtime.SelectedTaskByUser[user.ID]) {
			a.state.Runtime.SelectedTaskByUser[user.ID] = nextSeminar.TaskIDs[0]
		}
		seminar = nextSeminar
		a.appendEventLocked(user, sessionID, "seminar.selected", map[string]any{"seminarId": nextSeminar.ID}, nextSeminar.ID, "", "", "", "", 0)
	case "select-task":
		taskID := sanitizeText(payloadString(payload, "taskId"), 80, false)
		if !contains(seminar.TaskIDs, taskID) {
			return PlatformRuntime{}, errors.New("Эта задача не относится к выбранному семинару.")
		}
		if _, ok := a.findTaskLocked(taskID); !ok {
			return PlatformRuntime{}, errors.New("Задача не найдена.")
		}
		a.state.Runtime.SelectedTaskByUser[user.ID] = taskID
		a.appendEventLocked(user, sessionID, "task.opened", map[string]any{"source": "student-ui"}, seminar.ID, "", taskID, "", "", 0)
	case "toggle-setting":
		if err := requireTeacher(user); err != nil {
			return PlatformRuntime{}, err
		}
		setting := payloadString(payload, "setting")
		runtime := a.state.Runtime.SeminarRuntime[seminar.ID]
		switch setting {
		case "leaderboardEnabled":
			runtime.Settings.LeaderboardEnabled = !runtime.Settings.LeaderboardEnabled
		case "autoValidationEnabled":
			runtime.Settings.AutoValidationEnabled = !runtime.Settings.AutoValidationEnabled
		case "notificationsEnabled":
			runtime.Settings.NotificationsEnabled = !runtime.Settings.NotificationsEnabled
		case "diagnosticsVisible":
			runtime.Settings.DiagnosticsVisible = !runtime.Settings.DiagnosticsVisible
		case "referenceSolutionVisible":
			runtime.Settings.ReferenceSolutionVisible = !runtime.Settings.ReferenceSolutionVisible
		case "submissionsFrozen":
			runtime.Settings.SubmissionsFrozen = !runtime.Settings.SubmissionsFrozen
		default:
			return PlatformRuntime{}, errors.New("Неизвестная настройка семинара.")
		}
		a.state.Runtime.SeminarRuntime[seminar.ID] = runtime
		a.appendEventLocked(user, sessionID, "seminar.setting.changed", map[string]any{"setting": setting}, seminar.ID, "", "", "", "", 0)
	case "toggle-seminar-status":
		if err := requireTeacher(user); err != nil {
			return PlatformRuntime{}, err
		}
		runtime := a.state.Runtime.SeminarRuntime[seminar.ID]
		if runtime.Status == SeminarLive {
			runtime.Status = SeminarClosed
		} else {
			runtime.Status = SeminarLive
		}
		a.state.Runtime.SeminarRuntime[seminar.ID] = runtime
		a.appendEventLocked(user, sessionID, "seminar.status.changed", map[string]any{"nextStatus": runtime.Status}, seminar.ID, "", "", "", "", 0)
	case "pick-student":
		if err := requireTeacher(user); err != nil {
			return PlatformRuntime{}, err
		}
		if len(seminar.StudentIDs) == 0 {
			return PlatformRuntime{}, errors.New("В семинаре нет студентов.")
		}
		index := int(time.Now().UnixNano() % int64(len(seminar.StudentIDs)))
		a.state.Runtime.LastPickedStudentID = seminar.StudentIDs[index]
		a.appendEventLocked(user, sessionID, "seminar.student.picked", map[string]any{"studentId": a.state.Runtime.LastPickedStudentID}, seminar.ID, "", "", "", "", 0)
	case "update-seminar-meta":
		if err := requireTeacher(user); err != nil {
			return PlatformRuntime{}, err
		}
		field := sanitizeText(payloadString(payload, "field"), 32, false)
		value := payloadString(payload, "value")
		meta := a.state.Runtime.SeminarMeta[seminar.ID]
		switch field {
		case "title":
			meta.Title, err = validateRequiredText("Название", value, maxTitleLength, false)
		case "description":
			meta.Description = validateOptionalText(value, maxDescriptionLength, true)
		case "accessCode":
			meta.AccessCode, err = validateAccessCodeValue(value)
		case "startTime":
			meta.StartTime, err = validateDateTimeValue("Начало", value)
		case "endTime":
			meta.EndTime, err = validateDateTimeValue("Окончание", value)
		default:
			return PlatformRuntime{}, errors.New("Неизвестное поле семинара.")
		}
		if err != nil {
			return PlatformRuntime{}, err
		}
		a.state.Runtime.SeminarMeta[seminar.ID] = meta
		a.appendEventLocked(user, sessionID, "seminar.meta.changed", map[string]any{"field": field}, seminar.ID, "", "", "", "", 0)
	case "create-seminar":
		if err := requireTeacher(user); err != nil {
			return PlatformRuntime{}, err
		}
		title, err := validateRequiredText("Название", payloadString(payload, "title"), maxTitleLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		description := validateOptionalText(payloadString(payload, "description"), maxDescriptionLength, true)
		templateID := sanitizeText(payloadString(payload, "templateId"), 80, false)
		if _, ok := a.findTemplateLocked(templateID); !ok {
			return PlatformRuntime{}, errors.New("Шаблон БД не найден.")
		}
		startTime, err := validateDateTimeValue("Старт", payloadString(payload, "startTime"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		startedAt, _ := time.Parse(time.RFC3339, startTime)
		createdSeminar := Seminar{
			ID:          createID("seminar"),
			Title:       title,
			Description: description,
			GroupID:     seminar.GroupID,
			TeacherID:   user.ID,
			TemplateID:  templateID,
			TaskIDs:     append([]string{}, seminar.TaskIDs...),
			StudentIDs:  append([]string{}, seminar.StudentIDs...),
			AccessCode:  fmt.Sprintf("AUTO-%s", strings.ToUpper(randomString(4))),
			StartTime:   startedAt.UTC().Format(time.RFC3339),
			EndTime:     startedAt.Add(90 * time.Minute).UTC().Format(time.RFC3339),
			Status:      SeminarScheduled,
			Settings:    seminar.Settings,
		}
		a.state.Runtime.CreatedSeminars = append(a.state.Runtime.CreatedSeminars, createdSeminar)
		a.appendEventLocked(user, sessionID, "seminar.created", map[string]any{"createdSeminarId": createdSeminar.ID, "templateId": createdSeminar.TemplateID}, seminar.ID, "", "", "", "", 0)
	case "create-task":
		if err := requireTeacher(user); err != nil {
			return PlatformRuntime{}, err
		}
		templateID := sanitizeText(payloadString(payload, "templateId"), 80, false)
		template, ok := a.findTemplateLocked(templateID)
		if !ok {
			return PlatformRuntime{}, errors.New("Шаблон БД не найден.")
		}
		datasetIDs, err := validateIDList(payloadStringArray(payload, "datasetIds"), maxDatasetCount)
		if err != nil {
			return PlatformRuntime{}, err
		}
		if len(datasetIDs) == 0 {
			for _, dataset := range template.Datasets {
				datasetIDs = append(datasetIDs, dataset.ID)
			}
		}
		for _, datasetID := range datasetIDs {
			if _, ok := findDatasetByID(template, datasetID); !ok {
				return PlatformRuntime{}, errors.New("В задаче указан неизвестный датасет.")
			}
		}
		title, err := validateRequiredText("Название задачи", payloadString(payload, "title"), maxTitleLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		description, err := validateRequiredText("Описание задачи", payloadString(payload, "description"), maxDescriptionLength, true)
		if err != nil {
			return PlatformRuntime{}, err
		}
		difficulty, err := validateDifficultyValue(payloadString(payload, "difficulty"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		taskType, err := validateTaskTypeValue(payloadString(payload, "taskType"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		starterSQL, err := validateSQLValue("Starter SQL", payloadString(payload, "starterSql"), maxSQLLength, true)
		if err != nil {
			return PlatformRuntime{}, err
		}
		expectedQuery, err := validateSQLValue("Эталонный запрос", payloadString(payload, "expectedQuery"), maxSQLLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		constructs := splitAndTrim(validateOptionalText(payloadString(payload, "constructsText"), maxConstructsLength, false), ",")
		hints := splitAndTrim(validateOptionalText(payloadString(payload, "hintsText"), maxHintsLength, true), "\n")
		createdTask := TaskDefinition{
			ID:             createID("task"),
			SeminarID:      seminar.ID,
			Title:          title,
			Description:    description,
			Difficulty:     difficulty,
			TaskType:       taskType,
			Constructs:     constructs,
			ValidationMode: ValidationResultMatch,
			TemplateID:     templateID,
			DatasetIDs:     datasetIDs,
			StarterSQL:     starterSQL,
			ExpectedQuery:  expectedQuery,
			ValidationConfig: ValidationConfig{
				OrderMatters:      true,
				ColumnNamesMatter: true,
				NumericTolerance:  0.001,
				MaxExecutionMs:    5000,
				MaxResultRows:     100,
				ForbiddenKeywords: []string{"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "ATTACH", "DETACH", "PRAGMA"},
			},
			Hints: hints,
		}
		a.state.Runtime.CreatedTasks = append(a.state.Runtime.CreatedTasks, createdTask)
		a.state.Runtime.SeminarTaskIDs[seminar.ID] = append(a.state.Runtime.SeminarTaskIDs[seminar.ID], createdTask.ID)
		a.appendEventLocked(user, sessionID, "task.created", map[string]any{"taskId": createdTask.ID}, seminar.ID, "", createdTask.ID, "", "", 0)
	case "assign-task":
		if err := requireTeacher(user); err != nil {
			return PlatformRuntime{}, err
		}
		taskID := sanitizeText(payloadString(payload, "taskId"), 80, false)
		if _, ok := a.findTaskLocked(taskID); !ok {
			return PlatformRuntime{}, errors.New("Задача не найдена.")
		}
		taskIDs := a.state.Runtime.SeminarTaskIDs[seminar.ID]
		if !contains(taskIDs, taskID) {
			a.state.Runtime.SeminarTaskIDs[seminar.ID] = append(taskIDs, taskID)
		}
		a.appendEventLocked(user, sessionID, "task.assigned", map[string]any{"taskId": taskID}, seminar.ID, "", taskID, "", "", 0)
	case "remove-task":
		if err := requireTeacher(user); err != nil {
			return PlatformRuntime{}, err
		}
		taskID := sanitizeText(payloadString(payload, "taskId"), 80, false)
		if _, ok := a.findTaskLocked(taskID); !ok {
			return PlatformRuntime{}, errors.New("Задача не найдена.")
		}
		a.state.Runtime.SeminarTaskIDs[seminar.ID] = filterStrings(a.state.Runtime.SeminarTaskIDs[seminar.ID], taskID)
		a.appendEventLocked(user, sessionID, "task.removed", map[string]any{"taskId": taskID}, seminar.ID, "", taskID, "", "", 0)
	case "create-group":
		if err := requireAdmin(user); err != nil {
			return PlatformRuntime{}, err
		}
		title, err := validateGroupTitleValue(payloadString(payload, "title"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		stream, err := validateGroupStreamValue(payloadString(payload, "stream"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		group := Group{
			ID:     createID("group"),
			Title:  title,
			Stream: stream,
		}
		a.state.Runtime.CreatedGroups = append(a.state.Runtime.CreatedGroups, group)
		a.appendEventLocked(user, sessionID, "group.created", map[string]any{"groupId": group.ID}, "", "", "", "", "", 0)
	case "update-group":
		if err := requireAdmin(user); err != nil {
			return PlatformRuntime{}, err
		}
		groupID := sanitizeText(payloadString(payload, "groupId"), 80, false)
		group, ok := a.findGroupLocked(groupID)
		if !ok {
			return PlatformRuntime{}, errors.New("Группа не найдена.")
		}
		title, err := validateGroupTitleValue(payloadString(payload, "title"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		stream, err := validateGroupStreamValue(payloadString(payload, "stream"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		group.Title = title
		group.Stream = stream
		a.state.Runtime.GroupOverrides[groupID] = group
		for index, createdGroup := range a.state.Runtime.CreatedGroups {
			if createdGroup.ID == groupID {
				a.state.Runtime.CreatedGroups[index] = group
				delete(a.state.Runtime.GroupOverrides, groupID)
				break
			}
		}
		a.appendEventLocked(user, sessionID, "group.updated", map[string]any{"groupId": groupID}, "", "", "", "", "", 0)
	case "delete-group":
		if err := requireAdmin(user); err != nil {
			return PlatformRuntime{}, err
		}
		groupID := sanitizeText(payloadString(payload, "groupId"), 80, false)
		if _, ok := a.findGroupLocked(groupID); !ok {
			return PlatformRuntime{}, errors.New("Группа не найдена.")
		}
		for _, candidate := range a.resolvedUsersLocked() {
			if candidate.GroupID == groupID {
				return PlatformRuntime{}, errors.New("Нельзя удалить группу, пока в ней есть пользователи.")
			}
		}
		a.state.Runtime.CreatedGroups = filterGroups(a.state.Runtime.CreatedGroups, groupID)
		delete(a.state.Runtime.GroupOverrides, groupID)
		if !contains(a.state.Runtime.DeletedGroupIDs, groupID) {
			a.state.Runtime.DeletedGroupIDs = append(a.state.Runtime.DeletedGroupIDs, groupID)
		}
		a.appendEventLocked(user, sessionID, "group.deleted", map[string]any{"groupId": groupID}, "", "", "", "", "", 0)
	case "create-user":
		if err := requireAdmin(user); err != nil {
			return PlatformRuntime{}, err
		}
		role := Role(sanitizeText(payloadString(payload, "role"), 16, false))
		if role != RoleStudent && role != RoleTeacher && role != RoleAdmin {
			return PlatformRuntime{}, errors.New("Некорректная роль пользователя.")
		}
		fullName, err := validateRequiredText("ФИО", payloadString(payload, "fullName"), 120, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		login, err := validateLoginValue(payloadString(payload, "login"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		if _, exists := a.findUserByLogin(login); exists {
			return PlatformRuntime{}, errors.New("Пользователь с таким логином уже существует.")
		}
		groupID := ""
		if role == RoleStudent {
			groupID = sanitizeText(payloadString(payload, "groupId"), 80, false)
			if groupID == "" {
				return PlatformRuntime{}, errors.New("Для студента нужно выбрать группу.")
			}
			if _, ok := a.findGroupLocked(groupID); !ok {
				return PlatformRuntime{}, errors.New("Группа не найдена.")
			}
		}
		passwordHash := hashPassword(randomString(16))
		if role != RoleStudent {
			password, err := validatePasswordValue(payloadString(payload, "password"))
			if err != nil {
				return PlatformRuntime{}, err
			}
			passwordHash = hashPassword(password)
		}
		newUser := User{
			ID:           createID(string(role)),
			FullName:     fullName,
			Login:        login,
			PasswordHash: passwordHash,
			Role:         role,
			GroupID:      groupID,
			CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		}
		a.state.Runtime.CreatedUsers = append(a.state.Runtime.CreatedUsers, newUser)
		a.appendEventLocked(user, sessionID, "user.created", map[string]any{"userId": newUser.ID, "role": newUser.Role}, "", "", "", "", "", 0)
	case "update-user":
		if err := requireAdmin(user); err != nil {
			return PlatformRuntime{}, err
		}
		userID := sanitizeText(payloadString(payload, "userId"), 80, false)
		targetUser, ok := a.findUserByID(userID)
		if !ok {
			return PlatformRuntime{}, errors.New("Пользователь не найден.")
		}
		fullName, err := validateRequiredText("ФИО", payloadString(payload, "fullName"), 120, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		login, err := validateLoginValue(payloadString(payload, "login"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		if existing, exists := a.findUserByLogin(login); exists && existing.ID != userID {
			return PlatformRuntime{}, errors.New("Пользователь с таким логином уже существует.")
		}
		groupID := ""
		if targetUser.Role == RoleStudent {
			groupID = sanitizeText(payloadString(payload, "groupId"), 80, false)
			if groupID == "" {
				return PlatformRuntime{}, errors.New("Для студента нужно выбрать группу.")
			}
			if _, ok := a.findGroupLocked(groupID); !ok {
				return PlatformRuntime{}, errors.New("Группа не найдена.")
			}
		}
		targetUser.FullName = fullName
		targetUser.Login = login
		targetUser.GroupID = groupID
		if targetUser.Role != RoleStudent {
			password := sanitizeText(payloadString(payload, "password"), maxPasswordLength, false)
			if password != "" {
				validatedPassword, err := validatePasswordValue(password)
				if err != nil {
					return PlatformRuntime{}, err
				}
				targetUser.PasswordHash = hashPassword(validatedPassword)
			}
		}
		a.state.Runtime.UserOverrides[userID] = targetUser
		for index, createdUser := range a.state.Runtime.CreatedUsers {
			if createdUser.ID == userID {
				a.state.Runtime.CreatedUsers[index] = targetUser
				delete(a.state.Runtime.UserOverrides, userID)
				break
			}
		}
		a.appendEventLocked(user, sessionID, "user.updated", map[string]any{"userId": userID}, "", "", "", "", "", 0)
	case "delete-user":
		if err := requireAdmin(user); err != nil {
			return PlatformRuntime{}, err
		}
		userID := sanitizeText(payloadString(payload, "userId"), 80, false)
		targetUser, ok := a.findUserByID(userID)
		if !ok {
			return PlatformRuntime{}, errors.New("Пользователь не найден.")
		}
		if targetUser.ID == user.ID {
			return PlatformRuntime{}, errors.New("Нельзя удалить текущего администратора.")
		}
		if targetUser.Role != RoleStudent {
			for _, candidate := range a.resolvedSeminarsLocked() {
				if candidate.TeacherID == targetUser.ID {
					return PlatformRuntime{}, errors.New("Нельзя удалить преподавателя, пока за ним закреплены семинары.")
				}
			}
		}
		a.state.Runtime.CreatedUsers = filterUsers(a.state.Runtime.CreatedUsers, userID)
		delete(a.state.Runtime.UserOverrides, userID)
		if !contains(a.state.Runtime.DeletedUserIDs, userID) {
			a.state.Runtime.DeletedUserIDs = append(a.state.Runtime.DeletedUserIDs, userID)
		}
		for _, candidate := range a.resolvedSeminarsLocked() {
			filtered := filterStrings(candidate.StudentIDs, userID)
			if len(filtered) != len(candidate.StudentIDs) {
				a.state.Runtime.SeminarStudentIDs[candidate.ID] = filtered
			}
		}
		delete(a.state.Runtime.SelectedSeminarByUser, userID)
		delete(a.state.Runtime.SelectedTaskByUser, userID)
		for draftKey := range a.state.Runtime.Drafts {
			if strings.HasPrefix(draftKey, userID+":") {
				delete(a.state.Runtime.Drafts, draftKey)
			}
		}
		a.appendEventLocked(user, sessionID, "user.deleted", map[string]any{"userId": userID, "role": targetUser.Role}, "", "", "", "", "", 0)
	case "select-playground-template":
		templateID := sanitizeText(payloadString(payload, "templateId"), 80, false)
		template, ok := a.findTemplateLocked(templateID)
		if !ok {
			return PlatformRuntime{}, errors.New("Шаблон не найден.")
		}
		challenge := a.firstTemplateChallengeLocked(template.ID)
		updatePlaygroundSelection(PlaygroundSelection{
			SelectedPlaygroundTemplateID:  template.ID,
			SelectedPlaygroundChallengeID: challenge.ID,
			SelectedPlaygroundDatasetID:   firstDatasetID(template),
		})
	case "select-playground-dataset":
		datasetID := sanitizeText(payloadString(payload, "datasetId"), 80, false)
		currentTemplate, ok := a.findTemplateLocked(a.state.UserPlaygroundSelections[user.ID].SelectedPlaygroundTemplateID)
		if ok {
			if _, exists := findDatasetByID(currentTemplate, datasetID); !exists {
				return PlatformRuntime{}, errors.New("Датасет playground не найден.")
			}
		}
		updatePlaygroundSelection(PlaygroundSelection{SelectedPlaygroundDatasetID: datasetID})
	case "select-challenge":
		challenge, err := a.findChallengeLocked(sanitizeText(payloadString(payload, "challengeId"), 120, false))
		if err != nil {
			return PlatformRuntime{}, err
		}
		updatePlaygroundSelection(PlaygroundSelection{
			SelectedPlaygroundChallengeID: challenge.ID,
			SelectedPlaygroundTemplateID:  challenge.TemplateID,
			SelectedPlaygroundDatasetID:   firstString(challenge.DatasetIDs),
		})
		a.appendEventLocked(user, sessionID, "playground.challenge.opened", map[string]any{"templateId": challenge.TemplateID}, "", challenge.ID, "", "", "", 0)
	case "run-seminar-query":
		if seminar.Status != SeminarLive {
			return PlatformRuntime{}, errors.New("Семинар сейчас закрыт для выполнения запросов.")
		}
		taskID := sanitizeText(payloadString(payload, "taskId"), 80, false)
		if !contains(seminar.TaskIDs, taskID) {
			return PlatformRuntime{}, errors.New("Эта задача не относится к текущему семинару.")
		}
		task, ok := a.findTaskLocked(taskID)
		if !ok {
			return PlatformRuntime{}, errors.New("Задача не найдена.")
		}
		template, ok := a.findTemplateLocked(task.TemplateID)
		if !ok {
			return PlatformRuntime{}, errors.New("Шаблон не найден.")
		}
		dataset, ok := findDatasetByID(template, firstString(task.DatasetIDs))
		if !ok {
			return PlatformRuntime{}, errors.New("Датасет не найден.")
		}
		sqlText, err := validateSQLValue("SQL-запрос", payloadString(payload, "sqlText"), maxSQLLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		a.appendEventLocked(user, sessionID, "query.run", map[string]any{"datasetId": dataset.ID, "context": "seminar"}, seminar.ID, "", task.ID, sqlText, "", 0)
		execution := executeSQL(dataset.InitSQL, sqlText, task.ValidationConfig.MaxResultRows)
		queryRun := QueryRun{
			ID:              createID("run"),
			UserID:          user.ID,
			Role:            user.Role,
			Context:         "seminar",
			SeminarID:       seminar.ID,
			TaskID:          task.ID,
			DatasetID:       dataset.ID,
			SQLText:         sqlText,
			ExecutionTimeMs: execution.ExecutionTimeMs,
			RowCount:        execution.RowCount,
			Result:          execution.Result,
			CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		}
		if execution.OK {
			queryRun.Status = "success"
		} else {
			queryRun.Status = runtimeErrorStatus(execution.ErrorMessage)
			queryRun.ErrorMessage = execution.ErrorMessage
		}
		a.state.Runtime.QueryRuns = append(a.state.Runtime.QueryRuns, queryRun)
	case "submit-seminar-query":
		if seminar.Status != SeminarLive {
			return PlatformRuntime{}, errors.New("Семинар сейчас закрыт для отправки решений.")
		}
		if seminar.Settings.SubmissionsFrozen {
			return PlatformRuntime{}, errors.New("Отправки по семинару сейчас заморожены преподавателем.")
		}
		if !seminar.Settings.AutoValidationEnabled {
			return PlatformRuntime{}, errors.New("Автопроверка сейчас отключена преподавателем.")
		}
		taskID := sanitizeText(payloadString(payload, "taskId"), 80, false)
		if !contains(seminar.TaskIDs, taskID) {
			return PlatformRuntime{}, errors.New("Эта задача не относится к текущему семинару.")
		}
		task, ok := a.findTaskLocked(taskID)
		if !ok {
			return PlatformRuntime{}, errors.New("Задача не найдена.")
		}
		template, ok := a.findTemplateLocked(task.TemplateID)
		if !ok {
			return PlatformRuntime{}, errors.New("Шаблон не найден.")
		}
		datasets := datasetsByIDs(template, task.DatasetIDs)
		sqlText, err := validateSQLValue("SQL-запрос", payloadString(payload, "sqlText"), maxSQLLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		validation := validateAgainstDatasets(sqlText, task, datasets)
		submission := Submission{
			ID:              createID("submission"),
			UserID:          user.ID,
			SeminarID:       seminar.ID,
			TaskID:          task.ID,
			SQLText:         sqlText,
			SubmittedAt:     time.Now().UTC().Format(time.RFC3339),
			Status:          validation.Status,
			ExecutionTimeMs: validation.ExecutionTimeMs,
			ValidationDetails: SubmissionValidationDetails{
				Datasets: validation.Details,
				Summary:  validation.Summary,
			},
		}
		previousSubmissions := append([]Submission{}, a.state.Runtime.Submissions...)
		a.state.Runtime.Submissions = append(a.state.Runtime.Submissions, submission)
		wasAlreadySolved := hasSolved(previousSubmissions, user.ID, task.ID)
		taskAttemptCount := countAttempts(previousSubmissions, user.ID, task.ID) + 1
		firstSolver := !hasAnySolved(previousSubmissions, task.ID)
		solvedTaskIDs := solvedTaskSet(a.state.Runtime.Submissions, user.ID)
		a.appendEventLocked(user, sessionID, "submission."+validation.Status, map[string]any{
			"datasetsPassed": lenPassed(validation.Details),
			"datasetsTotal":  len(validation.Details),
		}, seminar.ID, "", task.ID, sqlText, validation.Status, validation.ExecutionTimeMs)
		if seminar.Settings.NotificationsEnabled {
			if validation.Status == "correct" && !wasAlreadySolved {
				title := "Новая успешная сдача"
				level := "info"
				if firstSolver {
					title = "Первое решение задачи"
					level = "success"
				}
				a.state.Runtime.Notifications = append(a.state.Runtime.Notifications, Notification{
					ID:        createID("notification"),
					SeminarID: seminar.ID,
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
					Level:     level,
					Title:     title,
					Body:      fmt.Sprintf("%s решил(а) задачу \"%s\".", user.FullName, task.Title),
				})
			}
			if taskAttemptCount >= 5 && validation.Status != "correct" {
				a.state.Runtime.Notifications = append(a.state.Runtime.Notifications, Notification{
					ID:        createID("notification"),
					SeminarID: seminar.ID,
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
					Level:     "warning",
					Title:     "Подозрительно много попыток",
					Body:      fmt.Sprintf("%s уже сделал(а) %d попыток по задаче \"%s\".", user.FullName, taskAttemptCount, task.Title),
				})
			}
			if validation.Status == "correct" && len(solvedTaskIDs) == len(seminar.TaskIDs) {
				a.state.Runtime.Notifications = append(a.state.Runtime.Notifications, Notification{
					ID:        createID("notification"),
					SeminarID: seminar.ID,
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
					Level:     "success",
					Title:     "Семинар завершён студентом",
					Body:      fmt.Sprintf("%s закрыл(а) все задачи текущего семинара.", user.FullName),
				})
			}
		}
	case "run-playground-query":
		challenge, err := a.findChallengeLocked(sanitizeText(payloadString(payload, "challengeId"), 120, false))
		if err != nil {
			return PlatformRuntime{}, err
		}
		template, ok := a.findTemplateLocked(challenge.TemplateID)
		if !ok {
			return PlatformRuntime{}, errors.New("Шаблон не найден.")
		}
		selection := a.state.UserPlaygroundSelections[user.ID]
		datasetID := selection.SelectedPlaygroundDatasetID
		if datasetID == "" {
			datasetID = firstString(challenge.DatasetIDs)
		}
		dataset, ok := findDatasetByID(template, datasetID)
		if !ok {
			dataset, ok = findDatasetByID(template, firstDatasetID(template))
			if !ok {
				return PlatformRuntime{}, errors.New("Датасет не найден.")
			}
		}
		sqlText, err := validateSQLValue("SQL-запрос", payloadString(payload, "sqlText"), maxSQLLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		execution := executeSQL(dataset.InitSQL, sqlText, 60)
		queryRun := QueryRun{
			ID:                    createID("run"),
			UserID:                user.ID,
			Role:                  user.Role,
			Context:               "playground",
			PlaygroundChallengeID: challenge.ID,
			DatasetID:             dataset.ID,
			SQLText:               sqlText,
			ExecutionTimeMs:       execution.ExecutionTimeMs,
			RowCount:              execution.RowCount,
			Result:                execution.Result,
			CreatedAt:             time.Now().UTC().Format(time.RFC3339),
		}
		if execution.OK {
			queryRun.Status = "success"
		} else {
			queryRun.Status = runtimeErrorStatus(execution.ErrorMessage)
			queryRun.ErrorMessage = execution.ErrorMessage
		}
		a.state.Runtime.QueryRuns = append(a.state.Runtime.QueryRuns, queryRun)
		a.appendEventLocked(user, sessionID, "playground.query.run", map[string]any{"datasetId": dataset.ID}, "", challenge.ID, "", sqlText, queryRun.Status, queryRun.ExecutionTimeMs)
	case "validate-playground-challenge":
		challenge, err := a.findChallengeLocked(sanitizeText(payloadString(payload, "challengeId"), 120, false))
		if err != nil {
			return PlatformRuntime{}, err
		}
		template, ok := a.findTemplateLocked(challenge.TemplateID)
		if !ok {
			return PlatformRuntime{}, errors.New("Шаблон не найден.")
		}
		taskLike := TaskDefinition{
			ID:             challenge.ID,
			SeminarID:      seminar.ID,
			Title:          challenge.Title,
			Description:    challenge.Description,
			Difficulty:     challenge.Difficulty,
			TaskType:       challenge.Topic,
			Constructs:     challenge.Constructs,
			ValidationMode: firstValidationMode(challenge.ValidationMode),
			TemplateID:     challenge.TemplateID,
			DatasetIDs:     challenge.DatasetIDs,
			StarterSQL:     challenge.StarterSQL,
			ExpectedQuery:  challenge.ExpectedQuery,
			ValidationConfig: ValidationConfig{
				OrderMatters:      true,
				ColumnNamesMatter: true,
				NumericTolerance:  0.001,
				MaxExecutionMs:    5000,
				MaxResultRows:     60,
				ForbiddenKeywords: challengeForbiddenKeywords(challenge.ValidationMode),
			},
			ValidationSpec: challenge.ValidationSpec,
			Hints:          []string{},
		}
		sqlText, err := validateSQLValue("SQL-запрос", payloadString(payload, "sqlText"), maxSQLLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		validation := validateAgainstDatasets(sqlText, taskLike, datasetsByIDs(template, challenge.DatasetIDs))
		a.state.Runtime.Notifications = append(a.state.Runtime.Notifications, Notification{
			ID:        createID("notification"),
			SeminarID: seminar.ID,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Level:     notificationLevel(validation.Status),
			Title:     "Playground: " + challenge.Title,
			Body:      validation.Summary,
		})
		a.appendEventLocked(user, sessionID, "playground.validation."+validation.Status, map[string]any{"summary": validation.Summary}, "", challenge.ID, "", sqlText, validation.Status, validation.ExecutionTimeMs)
	case "import-template":
		if err := requireAdmin(user); err != nil {
			return PlatformRuntime{}, err
		}
		title, err := validateRequiredText("Название шаблона", payloadString(payload, "title"), maxTitleLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		description := validateOptionalText(payloadString(payload, "description"), maxDescriptionLength, true)
		schemaSQL, err := validateSQLValue("SQL-схема", payloadString(payload, "schemaSql"), maxImportSQLLength, false)
		if err != nil {
			return PlatformRuntime{}, err
		}
		level, err := validateDifficultyValue(payloadString(payload, "level"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		datasets, err := validateImportDatasets(payloadDatasetInputs(payload, "datasets"))
		if err != nil {
			return PlatformRuntime{}, err
		}
		template, err := importTemplateFromSQL(importTemplateInput{
			Title:       title,
			Description: description,
			SchemaSQL:   schemaSQL,
			Level:       level,
			Datasets:    datasets,
		})
		if err != nil {
			return PlatformRuntime{}, err
		}
		a.state.Runtime.ImportedTemplates = append(a.state.Runtime.ImportedTemplates, template)
		a.appendEventLocked(user, sessionID, "template.imported", map[string]any{"templateId": template.ID, "tableCount": len(template.Tables)}, "", "", "", "", "", 0)
	default:
		return PlatformRuntime{}, errors.New("Unknown action")
	}

	runtime := a.buildRuntimeForUserLocked(user.ID, sessionID)
	if err := a.saveStateLocked(); err != nil {
		return PlatformRuntime{}, err
	}
	go a.broadcastState()
	return runtime, nil
}

func (a *app) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.isAllowedRequestOrigin(r) {
		http.Error(w, "origin forbidden", http.StatusForbidden)
		return
	}
	if allowed, retryAfter := a.wsLimiter.allow("ws:"+clientIP(r), rateLimitPolicy{Limit: 25, Window: time.Minute, Lockout: 5 * time.Minute}); !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
		http.Error(w, "too many websocket connections", http.StatusTooManyRequests)
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	userID, sessionID, err := a.verifyToken(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &client{conn: conn, userID: userID, sessionID: sessionID}
	a.clientsMu.Lock()
	a.clients[client] = struct{}{}
	a.clientsMu.Unlock()

	a.mu.RLock()
	catalog, runtime, err := a.buildResponseForUserLocked(userID, sessionID)
	a.mu.RUnlock()
	if err == nil {
		a.sendToClient(client, catalog, runtime)
	}

	go func() {
		defer func() {
			a.clientsMu.Lock()
			delete(a.clients, client)
			a.clientsMu.Unlock()
			_ = conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

func (a *app) broadcastState() {
	a.clientsMu.RLock()
	clients := make([]*client, 0, len(a.clients))
	for client := range a.clients {
		clients = append(clients, client)
	}
	a.clientsMu.RUnlock()

	for _, client := range clients {
		a.mu.RLock()
		catalog, runtime, err := a.buildResponseForUserLocked(client.userID, client.sessionID)
		a.mu.RUnlock()
		if err == nil {
			a.sendToClient(client, catalog, runtime)
		}
	}
}

func (a *app) sendToClient(client *client, catalog CatalogData, runtime PlatformRuntime) {
	client.mu.Lock()
	defer client.mu.Unlock()
	_ = client.conn.WriteJSON(WSMessage{
		Type:    "state:update",
		Catalog: catalog,
		Runtime: runtime,
	})
}

func (a *app) handleStatic(w http.ResponseWriter, r *http.Request) {
	distDir := filepath.Join("..", "dist")
	if r.URL.Path == "/" {
		http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
		return
	}

	cleanPath := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	requestedFile := filepath.Join(distDir, cleanPath)
	rel, err := filepath.Rel(distDir, requestedFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(requestedFile); err == nil && !info.IsDir() {
		http.ServeFile(w, r, requestedFile)
		return
	}
	http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
}

func (a *app) findUserByLogin(login string) (User, bool) {
	for _, user := range a.resolvedUsersLocked() {
		if user.Login == login {
			return user, true
		}
	}
	return User{}, false
}

func (a *app) findUserByID(id string) (User, bool) {
	for _, user := range a.resolvedUsersLocked() {
		if user.ID == id {
			return user, true
		}
	}
	return User{}, false
}

func (a *app) findStudentBySurname(surname string) (User, bool) {
	needle := strings.ToLower(strings.TrimSpace(surname))
	if needle == "" {
		return User{}, false
	}

	for _, user := range a.resolvedUsersLocked() {
		if user.Role != RoleStudent {
			continue
		}

		parts := strings.Fields(strings.ToLower(user.FullName))
		if len(parts) == 0 {
			continue
		}

		lastName := parts[len(parts)-1]
		if lastName == needle {
			return user, true
		}
	}

	return User{}, false
}

func (a *app) resolvedUsersLocked() []User {
	users := make([]User, 0, len(a.seed.Users)+len(a.state.Runtime.CreatedUsers))
	for _, user := range a.seed.Users {
		if contains(a.state.Runtime.DeletedUserIDs, user.ID) {
			continue
		}
		if override, ok := a.state.Runtime.UserOverrides[user.ID]; ok {
			users = append(users, override)
			continue
		}
		users = append(users, user)
	}
	for _, user := range a.state.Runtime.CreatedUsers {
		if contains(a.state.Runtime.DeletedUserIDs, user.ID) {
			continue
		}
		if override, ok := a.state.Runtime.UserOverrides[user.ID]; ok {
			users = append(users, override)
			continue
		}
		users = append(users, user)
	}
	return users
}

func (a *app) resolvedGroupsLocked() []Group {
	groups := make([]Group, 0, len(a.seed.Groups)+len(a.state.Runtime.CreatedGroups))
	for _, group := range a.seed.Groups {
		if contains(a.state.Runtime.DeletedGroupIDs, group.ID) {
			continue
		}
		if override, ok := a.state.Runtime.GroupOverrides[group.ID]; ok {
			groups = append(groups, override)
			continue
		}
		groups = append(groups, group)
	}
	for _, group := range a.state.Runtime.CreatedGroups {
		if contains(a.state.Runtime.DeletedGroupIDs, group.ID) {
			continue
		}
		if override, ok := a.state.Runtime.GroupOverrides[group.ID]; ok {
			groups = append(groups, override)
			continue
		}
		groups = append(groups, group)
	}
	return groups
}

func (a *app) findGroupLocked(id string) (Group, bool) {
	for _, group := range a.resolvedGroupsLocked() {
		if group.ID == id {
			return group, true
		}
	}
	return Group{}, false
}

func (a *app) findTaskLocked(id string) (TaskDefinition, bool) {
	for _, task := range a.seed.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	for _, task := range a.state.Runtime.CreatedTasks {
		if task.ID == id {
			return task, true
		}
	}
	return TaskDefinition{}, false
}

func (a *app) findTemplateLocked(id string) (DBTemplate, bool) {
	for _, template := range a.seed.Templates {
		if template.ID == id {
			return template, true
		}
	}
	for _, template := range a.state.Runtime.ImportedTemplates {
		if template.ID == id {
			return template, true
		}
	}
	return DBTemplate{}, false
}

func (a *app) firstTemplateChallengeLocked(templateID string) PlaygroundChallenge {
	for _, challenge := range a.seed.PlaygroundChallenges {
		if challenge.TemplateID == templateID {
			return challenge
		}
	}
	return PlaygroundChallenge{
		ID:            "playground-freeform-" + templateID,
		TemplateID:    templateID,
		Title:         "Freeform",
		Description:   "Свободная практика без заранее заданной автопроверки.",
		Difficulty:    "easy",
		Topic:         "Freeform",
		Constructs:    []string{"SELECT"},
		DatasetIDs:    []string{},
		StarterSQL:    "SELECT 1 AS ready;",
		ExpectedQuery: "SELECT 1 AS ready;",
		FeedbackMode:  "full",
	}
}

func (a *app) findChallengeLocked(challengeID string) (PlaygroundChallenge, error) {
	for _, challenge := range a.seed.PlaygroundChallenges {
		if challenge.ID == challengeID {
			return challenge, nil
		}
	}
	if strings.HasPrefix(challengeID, "playground-freeform-") {
		templateID := strings.TrimPrefix(challengeID, "playground-freeform-")
		template, ok := a.findTemplateLocked(templateID)
		if !ok {
			return PlaygroundChallenge{}, errors.New("Challenge не найден.")
		}
		return PlaygroundChallenge{
			ID:            challengeID,
			TemplateID:    templateID,
			Title:         "Freeform: " + template.Title,
			Description:   "Свободная практика без заранее заданной автопроверки.",
			Difficulty:    template.Level,
			Topic:         "Freeform",
			Constructs:    []string{"SELECT"},
			DatasetIDs:    collectDatasetIDs(template),
			StarterSQL:    "SELECT 1 AS ready;",
			ExpectedQuery: "SELECT 1 AS ready;",
			FeedbackMode:  "full",
		}, nil
	}
	return PlaygroundChallenge{}, errors.New("Challenge не найден.")
}

func (a *app) resolveSeminarLocked(seminar Seminar) Seminar {
	meta, hasMeta := a.state.Runtime.SeminarMeta[seminar.ID]
	runtime, hasRuntime := a.state.Runtime.SeminarRuntime[seminar.ID]
	studentIDs := a.state.Runtime.SeminarStudentIDs[seminar.ID]
	taskIDs := a.state.Runtime.SeminarTaskIDs[seminar.ID]
	if hasMeta {
		if meta.Title != "" {
			seminar.Title = meta.Title
		}
		if meta.Description != "" {
			seminar.Description = meta.Description
		}
		if meta.AccessCode != "" {
			seminar.AccessCode = meta.AccessCode
		}
		if meta.StartTime != "" {
			seminar.StartTime = meta.StartTime
		}
		if meta.EndTime != "" {
			seminar.EndTime = meta.EndTime
		}
	}
	if hasRuntime {
		seminar.Status = runtime.Status
		seminar.Settings = runtime.Settings
	}
	if len(studentIDs) > 0 {
		seminar.StudentIDs = append([]string{}, studentIDs...)
	}
	if len(taskIDs) > 0 {
		seminar.TaskIDs = append([]string{}, taskIDs...)
	}
	return seminar
}

func (a *app) resolvedSeminarsLocked() []Seminar {
	seminars := make([]Seminar, 0, len(a.seed.Seminars)+len(a.state.Runtime.CreatedSeminars))
	for _, seminar := range a.seed.Seminars {
		seminars = append(seminars, a.resolveSeminarLocked(seminar))
	}
	for _, seminar := range a.state.Runtime.CreatedSeminars {
		seminars = append(seminars, a.resolveSeminarLocked(seminar))
	}
	return seminars
}

func (a *app) findSeminarLocked(seminarID string) (Seminar, bool) {
	for _, seminar := range a.resolvedSeminarsLocked() {
		if seminar.ID == seminarID {
			return seminar, true
		}
	}
	return Seminar{}, false
}

func (a *app) canAccessSeminarLocked(user User, seminar Seminar) bool {
	if user.Role == RoleTeacher || user.Role == RoleAdmin {
		return true
	}
	return contains(seminar.StudentIDs, user.ID)
}

func (a *app) defaultSeminarForUserLocked(user User) Seminar {
	seminars := a.resolvedSeminarsLocked()
	for _, seminar := range seminars {
		if !a.canAccessSeminarLocked(user, seminar) {
			continue
		}
		if user.Role == RoleStudent && seminar.Status == SeminarLive {
			return seminar
		}
		if user.Role != RoleStudent && seminar.Status == SeminarLive {
			return seminar
		}
	}
	for _, seminar := range seminars {
		if a.canAccessSeminarLocked(user, seminar) {
			return seminar
		}
	}
	if len(seminars) > 0 {
		return seminars[0]
	}
	return a.resolveSeminarLocked(a.seed.Seminars[0])
}

func sanitizeUserForClient(user User) User {
	user.PasswordHash = ""
	return user
}

func sanitizeSeminarForStudent(seminar Seminar) Seminar {
	seminar.AccessCode = ""
	return seminar
}

func sanitizeTaskForStudent(task TaskDefinition, seminar Seminar) TaskDefinition {
	if !seminar.Settings.ReferenceSolutionVisible {
		task.StarterSQL = ""
		task.ExpectedQuery = ""
	}
	return task
}

func sanitizeChallengeForStudent(challenge PlaygroundChallenge) PlaygroundChallenge {
	challenge.StarterSQL = ""
	challenge.ExpectedQuery = ""
	return challenge
}

func (a *app) buildCatalogForUserLocked(user User) CatalogData {
	catalog := CatalogData{
		Users:                []User{},
		Groups:               []Group{},
		Templates:            []DBTemplate{},
		Seminars:             []Seminar{},
		Tasks:                []TaskDefinition{},
		PlaygroundChallenges: []PlaygroundChallenge{},
	}

	if user.Role == RoleTeacher || user.Role == RoleAdmin {
		for _, resolvedUser := range a.resolvedUsersLocked() {
			catalog.Users = append(catalog.Users, sanitizeUserForClient(resolvedUser))
		}
		catalog.Groups = append(catalog.Groups, a.resolvedGroupsLocked()...)
		catalog.Seminars = append(catalog.Seminars, a.resolvedSeminarsLocked()...)
		for _, task := range a.seed.Tasks {
			catalog.Tasks = append(catalog.Tasks, task)
		}
		catalog.Tasks = append(catalog.Tasks, a.state.Runtime.CreatedTasks...)
		for _, template := range a.seed.Templates {
			catalog.Templates = append(catalog.Templates, template)
		}
		catalog.Templates = append(catalog.Templates, a.state.Runtime.ImportedTemplates...)
		for _, challenge := range a.seed.PlaygroundChallenges {
			catalog.PlaygroundChallenges = append(catalog.PlaygroundChallenges, challenge)
		}
		return catalog
	}

	catalog.Users = append(catalog.Users, sanitizeUserForClient(user))
	if user.GroupID != "" {
		if group, ok := a.findGroupLocked(user.GroupID); ok {
			catalog.Groups = append(catalog.Groups, group)
		}
	}

	accessibleSeminars := []Seminar{}
	templateIDs := map[string]struct{}{}
	taskIDs := map[string]Seminar{}
	for _, seminar := range a.resolvedSeminarsLocked() {
		if !a.canAccessSeminarLocked(user, seminar) || seminar.Status != SeminarLive {
			continue
		}
		accessibleSeminars = append(accessibleSeminars, sanitizeSeminarForStudent(seminar))
		templateIDs[seminar.TemplateID] = struct{}{}
		for _, taskID := range seminar.TaskIDs {
			taskIDs[taskID] = seminar
		}
	}
	catalog.Seminars = accessibleSeminars

	for _, task := range a.seed.Tasks {
		if seminar, ok := taskIDs[task.ID]; ok {
			catalog.Tasks = append(catalog.Tasks, sanitizeTaskForStudent(task, seminar))
		}
	}
	for _, task := range a.state.Runtime.CreatedTasks {
		if seminar, ok := taskIDs[task.ID]; ok {
			catalog.Tasks = append(catalog.Tasks, sanitizeTaskForStudent(task, seminar))
		}
	}
	for _, template := range a.seed.Templates {
		if _, ok := templateIDs[template.ID]; ok {
			catalog.Templates = append(catalog.Templates, template)
		}
	}
	for _, template := range a.state.Runtime.ImportedTemplates {
		if _, ok := templateIDs[template.ID]; ok {
			catalog.Templates = append(catalog.Templates, template)
		}
	}
	for _, challenge := range a.seed.PlaygroundChallenges {
		if _, ok := templateIDs[challenge.TemplateID]; ok {
			catalog.PlaygroundChallenges = append(catalog.PlaygroundChallenges, sanitizeChallengeForStudent(challenge))
		}
	}

	return catalog
}

func (a *app) selectedSeminarForUserLocked(user User) Seminar {
	if seminarID := a.state.Runtime.SelectedSeminarByUser[user.ID]; seminarID != "" {
		if seminar, ok := a.findSeminarLocked(seminarID); ok && a.canAccessSeminarLocked(user, seminar) {
			return seminar
		}
	}
	seminar := a.defaultSeminarForUserLocked(user)
	a.state.Runtime.SelectedSeminarByUser[user.ID] = seminar.ID
	if len(seminar.TaskIDs) > 0 && !contains(seminar.TaskIDs, a.state.Runtime.SelectedTaskByUser[user.ID]) {
		a.state.Runtime.SelectedTaskByUser[user.ID] = seminar.TaskIDs[0]
	}
	return seminar
}

func (a *app) buildRuntimeForUserLocked(userID, sessionID string) PlatformRuntime {
	runtime := cloneRuntime(a.state.Runtime)
	runtime.IsAuthenticated = true
	runtime.CurrentUserID = userID
	runtime.SessionID = sessionID
	runtime.CreatedUsers = []User{}
	runtime.CreatedGroups = []Group{}
	runtime.UserOverrides = map[string]User{}
	runtime.GroupOverrides = map[string]Group{}
	runtime.DeletedUserIDs = []string{}
	runtime.DeletedGroupIDs = []string{}
	runtime.ImportedTemplates = []DBTemplate{}
	runtime.CreatedTasks = []TaskDefinition{}
	runtime.CreatedSeminars = []Seminar{}
	user, ok := a.findUserByID(userID)
	if ok {
		selectedSeminar := a.selectedSeminarForUserLocked(user)
		runtime.SelectedSeminarByUser[userID] = selectedSeminar.ID
	}
	if selection, ok := a.state.UserPlaygroundSelections[userID]; ok {
		if selection.SelectedPlaygroundTemplateID != "" {
			runtime.SelectedPlaygroundTemplateID = selection.SelectedPlaygroundTemplateID
		}
		if selection.SelectedPlaygroundChallengeID != "" {
			runtime.SelectedPlaygroundChallengeID = selection.SelectedPlaygroundChallengeID
		}
		if selection.SelectedPlaygroundDatasetID != "" {
			runtime.SelectedPlaygroundDatasetID = selection.SelectedPlaygroundDatasetID
		}
	}
	return runtime
}

func (a *app) buildResponseForUserLocked(userID, sessionID string) (CatalogData, PlatformRuntime, error) {
	user, ok := a.findUserByID(userID)
	if !ok {
		return CatalogData{}, PlatformRuntime{}, errors.New("Unknown user")
	}
	return a.buildCatalogForUserLocked(user), a.buildRuntimeForUserLocked(userID, sessionID), nil
}

func (a *app) upgradeUserPasswordHashLocked(userID, plainPassword string) {
	updated, ok := a.findUserByID(userID)
	if !ok {
		return
	}
	updated.PasswordHash = hashPassword(plainPassword)
	a.state.Runtime.UserOverrides[userID] = updated
}

func (a *app) appendEventLocked(user User, sessionID, eventType string, payload map[string]any, seminarID, playgroundChallengeID, taskID, sqlText, status string, executionTimeMs int) {
	safePayload := make(map[string]any, len(payload))
	for key, value := range payload {
		safeKey := sanitizeText(key, 64, false)
		switch typed := value.(type) {
		case string:
			safePayload[safeKey] = truncateForLog(typed, 240)
		default:
			safePayload[safeKey] = value
		}
	}

	a.state.Runtime.EventLogs = append(a.state.Runtime.EventLogs, EventLog{
		ID:                    createID("event"),
		UserID:                user.ID,
		Role:                  user.Role,
		SessionID:             sessionID,
		SeminarID:             seminarID,
		PlaygroundChallengeID: playgroundChallengeID,
		TaskID:                taskID,
		EventType:             sanitizeText(eventType, 80, false),
		SQLText:               truncateForLog(sqlText, 4000),
		Status:                sanitizeText(status, 32, false),
		ExecutionTimeMs:       executionTimeMs,
		Payload:               safePayload,
		CreatedAt:             time.Now().UTC().Format(time.RFC3339),
	})
}

func requireTeacher(user User) error {
	if user.Role == RoleTeacher || user.Role == RoleAdmin {
		return nil
	}
	return errors.New("Недостаточно прав для управления семинаром.")
}

func requireAdmin(user User) error {
	if user.Role == RoleAdmin {
		return nil
	}
	return errors.New("Недостаточно прав администратора.")
}

func randomString(length int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var builder strings.Builder
	for i := 0; i < length; i++ {
		index, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		builder.WriteByte(alphabet[index.Int64()])
	}
	return builder.String()
}

func splitAndTrim(value string, separator string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	raw := strings.Split(value, separator)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func payloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(typed)
	}
}

func payloadStringArray(payload map[string]any, key string) []string {
	value, ok := payload[key]
	if !ok || value == nil {
		return []string{}
	}
	raw, ok := value.([]any)
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		result = append(result, fmt.Sprint(item))
	}
	return result
}

type importDatasetInput struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	SeedSQL     string `json:"seedSql"`
}

func payloadDatasetInputs(payload map[string]any, key string) []importDatasetInput {
	value, ok := payload[key]
	if !ok || value == nil {
		return []importDatasetInput{}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return []importDatasetInput{}
	}
	var result []importDatasetInput
	_ = json.Unmarshal(data, &result)
	return result
}

func firstDatasetID(template DBTemplate) string {
	if len(template.Datasets) == 0 {
		return ""
	}
	return template.Datasets[0].ID
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func filterStrings(values []string, target string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func filterUsers(values []User, target string) []User {
	result := make([]User, 0, len(values))
	for _, value := range values {
		if value.ID != target {
			result = append(result, value)
		}
	}
	return result
}

func filterGroups(values []Group, target string) []Group {
	result := make([]Group, 0, len(values))
	for _, value := range values {
		if value.ID != target {
			result = append(result, value)
		}
	}
	return result
}

func runtimeErrorStatus(message string) string {
	if strings.Contains(strings.ToLower(message), "запрещ") {
		return "blocked"
	}
	return "error"
}

func datasetsByIDs(template DBTemplate, ids []string) []TemplateDataset {
	result := make([]TemplateDataset, 0)
	for _, id := range ids {
		if dataset, ok := findDatasetByID(template, id); ok {
			result = append(result, dataset)
		}
	}
	if len(result) == 0 {
		return template.Datasets
	}
	return result
}

func findDatasetByID(template DBTemplate, id string) (TemplateDataset, bool) {
	for _, dataset := range template.Datasets {
		if dataset.ID == id {
			return dataset, true
		}
	}
	return TemplateDataset{}, false
}

func hasSolved(submissions []Submission, userID, taskID string) bool {
	for _, submission := range submissions {
		if submission.UserID == userID && submission.TaskID == taskID && submission.Status == "correct" {
			return true
		}
	}
	return false
}

func hasAnySolved(submissions []Submission, taskID string) bool {
	for _, submission := range submissions {
		if submission.TaskID == taskID && submission.Status == "correct" {
			return true
		}
	}
	return false
}

func countAttempts(submissions []Submission, userID, taskID string) int {
	count := 0
	for _, submission := range submissions {
		if submission.UserID == userID && submission.TaskID == taskID {
			count++
		}
	}
	return count
}

func solvedTaskSet(submissions []Submission, userID string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, submission := range submissions {
		if submission.UserID == userID && submission.Status == "correct" {
			result[submission.TaskID] = struct{}{}
		}
	}
	return result
}

func lenPassed(details []DatasetValidationOutcome) int {
	count := 0
	for _, detail := range details {
		if detail.Passed {
			count++
		}
	}
	return count
}

func notificationLevel(status string) string {
	if status == "correct" {
		return "success"
	}
	return "info"
}

func firstValidationMode(mode ValidationMode) ValidationMode {
	if mode == "" {
		return ValidationResultMatch
	}
	return mode
}

func challengeForbiddenKeywords(mode ValidationMode) []string {
	if mode != "" && mode != ValidationResultMatch {
		return []string{"ATTACH", "DETACH", "PRAGMA"}
	}
	return []string{"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "ATTACH", "DETACH", "PRAGMA"}
}

func normaliseDateValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.Contains(trimmed, "T") && !strings.HasSuffix(trimmed, "Z") && !strings.Contains(trimmed, "+") {
		return trimmed + ":00Z"
	}
	return trimmed
}

type importTemplateInput struct {
	Title       string
	Description string
	SchemaSQL   string
	Level       string
	Datasets    []importDatasetInput
}

func importTemplateFromSQL(input importTemplateInput) (DBTemplate, error) {
	db, err := newSQLiteDB()
	if err != nil {
		return DBTemplate{}, err
	}
	defer db.Close()

	if err := executeStatements(db, input.SchemaSQL); err != nil {
		return DBTemplate{}, err
	}

	tablesResult, _, err := queryToTable(db, `
		SELECT name
		FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name ASC;
	`, 200)
	if err != nil {
		return DBTemplate{}, err
	}

	tableNames := make([]string, 0)
	if tablesResult != nil {
		for _, row := range tablesResult.Rows {
			tableNames = append(tableNames, fmt.Sprint(row[0]))
		}
	}
	if len(tableNames) > 60 {
		return DBTemplate{}, errors.New("В импортируемой схеме слишком много таблиц.")
	}

	tables := make([]TableDefinition, 0, len(tableNames))
	for index, tableName := range tableNames {
		columns, err := inspectColumns(db, tableName)
		if err != nil {
			return DBTemplate{}, err
		}
		if len(columns) > 80 {
			return DBTemplate{}, fmt.Errorf("Таблица %s содержит слишком много столбцов для безопасного импорта.", tableName)
		}
		tables = append(tables, TableDefinition{
			Name:        tableName,
			Description: "Импортировано из SQL-схемы: " + labelFromName(tableName) + ".",
			Position: TablePosition{
				X: 40 + (index%3)*280,
				Y: 40 + (index/3)*240,
			},
			Columns:    columns,
			SampleRows: [][]string{},
		})
	}

	topics := detectTopics(input.SchemaSQL, len(tables) > 0)
	datasets := input.Datasets
	if len(datasets) == 0 {
		datasets = []importDatasetInput{{
			Label:       "Imported dataset",
			Description: "Датасет, импортированный из SQL-скрипта.",
		}}
	}

	normalisedDatasets := make([]TemplateDataset, 0, len(datasets))
	for _, dataset := range datasets {
		initSQL := strings.TrimSpace(strings.Join([]string{input.SchemaSQL, dataset.SeedSQL}, "\n"))
		normalisedDatasets = append(normalisedDatasets, TemplateDataset{
			ID:          createID("dataset"),
			Label:       dataset.Label,
			Description: dataset.Description,
			SchemaSQL:   input.SchemaSQL,
			SeedSQL:     dataset.SeedSQL,
			InitSQL:     initSQL,
		})
	}

	return DBTemplate{
		ID:          createID("template"),
		Title:       input.Title,
		Description: input.Description,
		Level:       input.Level,
		Topics:      topics,
		Tables:      tables,
		Datasets:    normalisedDatasets,
	}, nil
}

func inspectColumns(db *sql.DB, tableName string) ([]TableColumn, error) {
	infoRows, _, err := queryToTable(db, fmt.Sprintf("PRAGMA table_info('%s')", tableName), 200)
	if err != nil {
		return nil, err
	}
	foreignRows, _, err := queryToTable(db, fmt.Sprintf("PRAGMA foreign_key_list('%s')", tableName), 200)
	if err != nil {
		return nil, err
	}

	foreignMap := make(map[string]TableReference)
	if foreignRows != nil {
		for _, row := range foreignRows.Rows {
			from := fmt.Sprint(row[3])
			foreignMap[from] = TableReference{
				Table:  fmt.Sprint(row[2]),
				Column: fmt.Sprint(row[4]),
			}
		}
	}

	columns := make([]TableColumn, 0)
	if infoRows != nil {
		for _, row := range infoRows.Rows {
			column := TableColumn{
				Name:         fmt.Sprint(row[1]),
				Type:         fmt.Sprint(row[2]),
				IsPrimaryKey: fmt.Sprint(row[5]) == "1",
			}
			if reference, ok := foreignMap[column.Name]; ok {
				column.References = &reference
			}
			columns = append(columns, column)
		}
	}
	return columns, nil
}

func labelFromName(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	for index, part := range parts {
		if part == "" {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func detectTopics(schemaSQL string, hasTables bool) []string {
	topics := make([]string, 0)
	if strings.Contains(strings.ToLower(schemaSQL), "join") {
		topics = append(topics, "JOIN")
	}
	if strings.Contains(strings.ToLower(schemaSQL), "with") {
		topics = append(topics, "CTE")
	}
	if strings.Contains(strings.ToLower(schemaSQL), "over(") || strings.Contains(strings.ToLower(schemaSQL), "over (") {
		topics = append(topics, "WINDOW")
	}
	if hasTables {
		topics = append(topics, "SELECT")
	}
	return topics
}

func collectDatasetIDs(template DBTemplate) []string {
	result := make([]string, 0, len(template.Datasets))
	for _, dataset := range template.Datasets {
		result = append(result, dataset.ID)
	}
	return result
}
