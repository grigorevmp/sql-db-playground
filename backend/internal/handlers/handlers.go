package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sql-seminar/backend/internal/auth"
	"github.com/sql-seminar/backend/internal/hub"
	"github.com/sql-seminar/backend/internal/models"
	"github.com/sql-seminar/backend/internal/store"
	"github.com/sql-seminar/backend/internal/validation"
)

type Server struct {
	pool  *pgxpool.Pool
	hub   *hub.Hub
	store *store.Store
}

func New(pool *pgxpool.Pool, h *hub.Hub, s *store.Store) *Server {
	return &Server{pool: pool, hub: h, store: s}
}

// ──────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint
}

func errJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func createID(prefix string) string {
	return fmt.Sprintf("%s-%s-%x", prefix, randStr(8), time.Now().UnixMilli())
}

func randStr(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func (s *Server) extractUser(r *http.Request) (*models.User, *auth.Claims, error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, nil, fmt.Errorf("missing token")
	}
	claims, err := auth.VerifyToken(strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		return nil, nil, err
	}
	user, err := s.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("unknown user")
	}
	return user, claims, nil
}

func (s *Server) broadcast(ctx context.Context) {
	// Build a generic runtime state and push to all connected clients
	rt, err := s.store.BuildRuntime(ctx, "", "")
	if err != nil {
		return
	}
	s.hub.BroadcastAll(rt)
}

func (s *Server) broadcastForUser(ctx context.Context, userID, sessionID string) {
	rt, err := s.store.BuildRuntime(ctx, userID, sessionID)
	if err != nil {
		return
	}
	s.hub.SendToUser(userID, rt)
	// also broadcast updated state to everyone (for teacher dashboard etc.)
	s.broadcast(ctx)
}

// ──────────────────────────────────────────────────────────────────
// Routes
// ──────────────────────────────────────────────────────────────────

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", s.Health)
	mux.HandleFunc("POST /api/auth/login", s.Login)
	mux.HandleFunc("GET /api/bootstrap", s.Bootstrap)
	mux.HandleFunc("POST /api/reset", s.Reset)
	mux.HandleFunc("POST /api/actions/{action}", s.Action)
	mux.HandleFunc("GET /ws", s.WebSocket)
}

// ──────────────────────────────────────────────────────────────────
// Handlers
// ──────────────────────────────────────────────────────────────────

func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errJSON(w, http.StatusBadRequest, "invalid body")
		return
	}

	user, err := s.store.GetUserByLogin(r.Context(), strings.TrimSpace(body.Login))
	if err != nil {
		errJSON(w, http.StatusUnauthorized, "Пользователь не найден.")
		return
	}

	if auth.HashPassword(body.Password) != user.PasswordHash {
		errJSON(w, http.StatusUnauthorized, "Неверный пароль.")
		return
	}

	sessionID := createID("session")
	token, err := auth.SignToken(user.ID, sessionID)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "token error")
		return
	}

	s.store.AppendEvent(r.Context(), &models.EventLog{
		ID:        createID("event"),
		UserID:    user.ID,
		Role:      user.Role,
		SessionID: sessionID,
		EventType: "auth.login",
		Payload:   map[string]interface{}{"login": user.Login},
		CreatedAt: time.Now(),
	})

	rt, _ := s.store.BuildRuntime(r.Context(), user.ID, sessionID)
	s.hub.BroadcastAll(rt)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":   token,
		"runtime": rt,
		"currentUser": models.PublicUser{
			ID:        user.ID,
			FullName:  user.FullName,
			Login:     user.Login,
			Role:      user.Role,
			GroupID:   user.GroupID,
			CreatedAt: user.CreatedAt,
		},
	})
}

func (s *Server) Bootstrap(w http.ResponseWriter, r *http.Request) {
	user, claims, err := s.extractUser(r)
	if err != nil {
		errJSON(w, http.StatusUnauthorized, err.Error())
		return
	}

	rt, err := s.store.BuildRuntime(r.Context(), user.ID, claims.SessionID)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"runtime": rt,
		"currentUser": models.PublicUser{
			ID:        user.ID,
			FullName:  user.FullName,
			Login:     user.Login,
			Role:      user.Role,
			GroupID:   user.GroupID,
			CreatedAt: user.CreatedAt,
		},
	})
}

func (s *Server) Reset(w http.ResponseWriter, r *http.Request) {
	user, claims, err := s.extractUser(r)
	if err != nil {
		errJSON(w, http.StatusUnauthorized, err.Error())
		return
	}
	if user.Role == models.RoleStudent {
		errJSON(w, http.StatusForbidden, "Недостаточно прав.")
		return
	}

	if err := s.store.Reset(r.Context()); err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.store.AppendEvent(r.Context(), &models.EventLog{
		ID:        createID("event"),
		UserID:    user.ID,
		Role:      user.Role,
		SessionID: claims.SessionID,
		EventType: "system.reset",
		Payload:   map[string]interface{}{},
		CreatedAt: time.Now(),
	})

	rt, _ := s.store.BuildRuntime(r.Context(), user.ID, claims.SessionID)
	s.hub.BroadcastAll(rt)
	writeJSON(w, http.StatusOK, map[string]interface{}{"runtime": rt})
}

func (s *Server) WebSocket(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	claims, err := auth.VerifyToken(tokenStr)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	rt, err := s.store.BuildRuntime(r.Context(), claims.UserID, claims.SessionID)
	if err != nil {
		http.Error(w, "state error", http.StatusInternalServerError)
		return
	}

	s.hub.ServeWS(w, r, claims.UserID, claims.SessionID, rt)
}

// ──────────────────────────────────────────────────────────────────
// Action dispatcher
// ──────────────────────────────────────────────────────────────────

func (s *Server) Action(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	user, claims, err := s.extractUser(r)
	if err != nil {
		errJSON(w, http.StatusUnauthorized, err.Error())
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		payload = map[string]interface{}{}
	}

	ctx := r.Context()

	if err := s.dispatchAction(ctx, action, payload, user, claims.SessionID); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	rt, _ := s.store.BuildRuntime(ctx, user.ID, claims.SessionID)
	s.hub.BroadcastAll(rt)
	writeJSON(w, http.StatusOK, map[string]interface{}{"runtime": rt})
}

func getString(payload map[string]interface{}, key string) string {
	if v, ok := payload[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func getBool(payload map[string]interface{}, key string) bool {
	if v, ok := payload[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func (s *Server) dispatchAction(ctx context.Context, action string, payload map[string]interface{}, user *models.User, sessionID string) error {
	seminar, err := s.store.GetActiveSeminar(ctx)
	if err != nil {
		// not all actions require a seminar
		seminar = &models.Seminar{}
	}

	switch action {
	// ── drafts ──────────────────────────────────────────────────────
	case "save-draft":
		return s.store.SaveDraft(ctx, user.ID, getString(payload, "key"), getString(payload, "value"))

	// ── task selection ──────────────────────────────────────────────
	case "select-task":
		taskID := getString(payload, "taskId")
		if err := s.store.SetSelectedTask(ctx, user.ID, seminar.ID, taskID); err != nil {
			return err
		}
		s.store.AppendEvent(ctx, &models.EventLog{
			ID:        createID("event"),
			UserID:    user.ID,
			Role:      user.Role,
			SessionID: sessionID,
			SeminarID: strPtr(seminar.ID),
			TaskID:    strPtr(taskID),
			EventType: "task.opened",
			Payload:   map[string]interface{}{"source": "student-ui"},
			CreatedAt: time.Now(),
		})
		return nil

	// ── seminar controls (teacher only) ─────────────────────────────
	case "toggle-setting":
		if user.Role == models.RoleStudent {
			return fmt.Errorf("Недостаточно прав для управления семинаром.")
		}
		setting := getString(payload, "setting")
		val := getBool(payload, "value")
		if err := s.store.ToggleSeminarSetting(ctx, seminar.ID, setting, val); err != nil {
			return err
		}
		s.store.AppendEvent(ctx, &models.EventLog{
			ID:        createID("event"),
			UserID:    user.ID,
			Role:      user.Role,
			SessionID: sessionID,
			SeminarID: strPtr(seminar.ID),
			EventType: "seminar.setting.changed",
			Payload:   map[string]interface{}{"setting": setting},
			CreatedAt: time.Now(),
		})
		return nil

	case "toggle-seminar-status":
		if user.Role == models.RoleStudent {
			return fmt.Errorf("Недостаточно прав для управления семинаром.")
		}
		return s.store.ToggleSeminarStatus(ctx, seminar.ID)

	case "pick-student":
		if user.Role == models.RoleStudent {
			return fmt.Errorf("Недостаточно прав.")
		}
		students, err := s.store.GetSeminarStudents(ctx, seminar.ID)
		if err != nil || len(students) == 0 {
			return fmt.Errorf("нет студентов в семинаре")
		}
		picked := students[rand.Intn(len(students))]
		s.store.SetLastPickedStudent(ctx, seminar.ID, picked)
		s.store.AppendEvent(ctx, &models.EventLog{
			ID:        createID("event"),
			UserID:    user.ID,
			Role:      user.Role,
			SessionID: sessionID,
			SeminarID: strPtr(seminar.ID),
			EventType: "seminar.student.picked",
			Payload:   map[string]interface{}{"studentId": picked},
			CreatedAt: time.Now(),
		})
		return nil

	case "update-seminar-meta":
		if user.Role == models.RoleStudent {
			return fmt.Errorf("Недостаточно прав.")
		}
		field := getString(payload, "field")
		value := getString(payload, "value")
		return s.store.UpdateSeminarMeta(ctx, seminar.ID, field, value)

	// ── create seminar ───────────────────────────────────────────────
	case "create-seminar":
		if user.Role == models.RoleStudent {
			return fmt.Errorf("Недостаточно прав.")
		}
		startTime, _ := time.Parse(time.RFC3339, getString(payload, "startTime"))
		newSeminar := &models.Seminar{
			ID:          createID("seminar"),
			Title:       getString(payload, "title"),
			Description: getString(payload, "description"),
			GroupID:     seminar.GroupID,
			TeacherID:   user.ID,
			TemplateID:  getString(payload, "templateId"),
			TaskIDs:     seminar.TaskIDs,
			StudentIDs:  seminar.StudentIDs,
			AccessCode:  "AUTO-" + strings.ToUpper(randStr(4)),
			StartTime:   startTime,
			EndTime:     startTime.Add(90 * time.Minute),
			Status:      models.SeminarScheduled,
			Settings:    seminar.Settings,
		}
		if err := s.store.CreateSeminar(ctx, newSeminar); err != nil {
			return err
		}
		s.store.AppendEvent(ctx, &models.EventLog{
			ID:        createID("event"),
			UserID:    user.ID,
			Role:      user.Role,
			SessionID: sessionID,
			SeminarID: strPtr(seminar.ID),
			EventType: "seminar.created",
			Payload:   map[string]interface{}{"createdSeminarId": newSeminar.ID},
			CreatedAt: time.Now(),
		})
		return nil

	// ── create task ──────────────────────────────────────────────────
	case "create-task":
		if user.Role == models.RoleStudent {
			return fmt.Errorf("Недостаточно прав.")
		}
		templateID := getString(payload, "templateId")
		tmpl, err := s.store.GetTemplateByID(ctx, templateID)
		if err != nil {
			return fmt.Errorf("Шаблон БД не найден.")
		}

		datasetIDs := tmpl.DatasetIDs()
		if dsArr, ok := payload["datasetIds"].([]interface{}); ok && len(dsArr) > 0 {
			datasetIDs = make([]string, len(dsArr))
			for i, v := range dsArr {
				datasetIDs[i] = fmt.Sprintf("%v", v)
			}
		}

		constructsText := getString(payload, "constructsText")
		var constructs []string
		for _, c := range strings.Split(constructsText, ",") {
			if t := strings.TrimSpace(c); t != "" {
				constructs = append(constructs, t)
			}
		}

		hintsText := getString(payload, "hintsText")
		var hints []string
		for _, h := range strings.Split(hintsText, "\n") {
			if t := strings.TrimSpace(h); t != "" {
				hints = append(hints, t)
			}
		}

		task := &models.TaskDefinition{
			ID:             createID("task"),
			SeminarID:      seminar.ID,
			Title:          getString(payload, "title"),
			Description:    getString(payload, "description"),
			Difficulty:     models.Difficulty(getString(payload, "difficulty")),
			TaskType:       getString(payload, "taskType"),
			Constructs:     constructs,
			ValidationMode: models.ValidationResultMatch,
			TemplateID:     templateID,
			DatasetIDs:     datasetIDs,
			StarterSql:     getString(payload, "starterSql"),
			ExpectedQuery:  getString(payload, "expectedQuery"),
			ValidationConfig: models.ValidationConfig{
				OrderMatters:      true,
				ColumnNamesMatter: true,
				NumericTolerance:  0.001,
				MaxExecutionMs:    5000,
				MaxResultRows:     100,
				ForbiddenKeywords: []string{"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "ATTACH", "DETACH", "PRAGMA"},
			},
			Hints: hints,
		}
		if err := s.store.CreateTask(ctx, task, seminar.ID); err != nil {
			return err
		}
		s.store.AppendEvent(ctx, &models.EventLog{
			ID:        createID("event"),
			UserID:    user.ID,
			Role:      user.Role,
			SessionID: sessionID,
			SeminarID: strPtr(seminar.ID),
			TaskID:    strPtr(task.ID),
			EventType: "task.created",
			Payload:   map[string]interface{}{"taskId": task.ID},
			CreatedAt: time.Now(),
		})
		return nil

	case "assign-task":
		if user.Role == models.RoleStudent {
			return fmt.Errorf("Недостаточно прав.")
		}
		return s.store.AssignTask(ctx, seminar.ID, getString(payload, "taskId"))

	case "remove-task":
		if user.Role == models.RoleStudent {
			return fmt.Errorf("Недостаточно прав.")
		}
		return s.store.RemoveTask(ctx, seminar.ID, getString(payload, "taskId"))

	// ── playground selections ────────────────────────────────────────
	case "select-playground-template":
		return s.store.UpdatePlaygroundSelection(ctx, user.ID, "template", getString(payload, "templateId"))

	case "select-playground-dataset":
		return s.store.UpdatePlaygroundSelection(ctx, user.ID, "dataset", getString(payload, "datasetId"))

	case "select-challenge":
		return s.store.UpdatePlaygroundSelection(ctx, user.ID, "challenge", getString(payload, "challengeId"))

	// ── run seminar query ────────────────────────────────────────────
	case "run-seminar-query":
		if seminar.Status != models.SeminarLive {
			return fmt.Errorf("Семинар сейчас закрыт для выполнения запросов.")
		}
		taskID := getString(payload, "taskId")
		sqlText := getString(payload, "sqlText")

		task, err := s.store.GetTaskByID(ctx, taskID)
		if err != nil {
			return fmt.Errorf("Задача не найдена.")
		}
		ds, err := s.store.GetDataset(ctx, task.DatasetIDs[0])
		if err != nil {
			return fmt.Errorf("Датасет не найден.")
		}

		result, execMs, execErr := validation.RunQuery(ctx, s.pool, ds.InitSql, sqlText, task.ValidationConfig.MaxResultRows)

		run := &models.QueryRun{
			ID:              createID("run"),
			UserID:          user.ID,
			Role:            user.Role,
			Context:         models.QueryContextSeminar,
			SeminarID:       strPtr(seminar.ID),
			TaskID:          strPtr(taskID),
			DatasetID:       ds.ID,
			SqlText:         sqlText,
			ExecutionTimeMs: execMs,
			CreatedAt:       time.Now(),
		}
		if execErr != nil {
			run.Status = "error"
			msg := execErr.Error()
			run.ErrorMessage = &msg
		} else {
			run.Status = "success"
			run.Result = result
			if result != nil {
				run.RowCount = len(result.Rows)
			}
		}
		s.store.SaveQueryRun(ctx, run)

		s.store.AppendEvent(ctx, &models.EventLog{
			ID:        createID("event"),
			UserID:    user.ID,
			Role:      user.Role,
			SessionID: sessionID,
			SeminarID: strPtr(seminar.ID),
			TaskID:    strPtr(taskID),
			EventType: "query.run",
			SqlText:   strPtr(sqlText),
			Status:    strPtr(run.Status),
			Payload:   map[string]interface{}{"context": "seminar"},
			CreatedAt: time.Now(),
		})
		return nil

	// ── submit seminar query ─────────────────────────────────────────
	case "submit-seminar-query":
		if seminar.Status != models.SeminarLive {
			return fmt.Errorf("Семинар сейчас закрыт для отправки решений.")
		}
		if seminar.Settings.SubmissionsFrozen {
			return fmt.Errorf("Отправки по семинару сейчас заморожены преподавателем.")
		}
		if !seminar.Settings.AutoValidationEnabled {
			return fmt.Errorf("Автопроверка сейчас отключена преподавателем.")
		}

		taskID := getString(payload, "taskId")
		sqlText := getString(payload, "sqlText")

		task, err := s.store.GetTaskByID(ctx, taskID)
		if err != nil {
			return fmt.Errorf("Задача не найдена.")
		}

		datasets, err := s.store.GetDatasetsByIDs(ctx, task.DatasetIDs)
		if err != nil {
			return fmt.Errorf("Датасеты не найдены.")
		}

		outcome := validation.ValidateAgainstDatasets(ctx, s.pool, sqlText, task, datasets)

		sub := &models.Submission{
			ID:              createID("submission"),
			UserID:          user.ID,
			SeminarID:       seminar.ID,
			TaskID:          taskID,
			SqlText:         sqlText,
			SubmittedAt:     time.Now(),
			Status:          outcome.Status,
			ExecutionTimeMs: outcome.ExecutionTimeMs,
			ValidationDetails: models.ValidationDetails{
				Datasets: outcome.Details,
				Summary:  outcome.Summary,
			},
		}
		if err := s.store.SaveSubmission(ctx, sub); err != nil {
			return err
		}

		// notifications
		if seminar.Settings.NotificationsEnabled {
			wasAlreadySolved, _ := s.store.WasPreviouslySolved(ctx, user.ID, taskID)
			firstSolver, _ := s.store.IsFirstSolver(ctx, taskID)
			attemptCount, _ := s.store.GetAttemptCount(ctx, user.ID, taskID)

			if outcome.Status == models.SubmissionCorrect && !wasAlreadySolved {
				level := "info"
				title := "Новая успешная сдача"
				if firstSolver {
					level = "success"
					title = "Первое решение задачи"
				}
				s.store.SaveNotification(ctx, &models.Notification{
					ID:        createID("notification"),
					SeminarID: seminar.ID,
					CreatedAt: time.Now(),
					Level:     level,
					Title:     title,
					Body:      fmt.Sprintf("%s решил(а) задачу \"%s\".", user.FullName, task.Title),
				})
			}

			if attemptCount >= 5 && outcome.Status != models.SubmissionCorrect {
				s.store.SaveNotification(ctx, &models.Notification{
					ID:        createID("notification"),
					SeminarID: seminar.ID,
					CreatedAt: time.Now(),
					Level:     "warning",
					Title:     "Подозрительно много попыток",
					Body:      fmt.Sprintf("%s уже сделал(а) %d попыток по задаче \"%s\".", user.FullName, attemptCount, task.Title),
				})
			}

			solvedAll, _ := s.store.SolvedAllTasks(ctx, user.ID, seminar.ID)
			if outcome.Status == models.SubmissionCorrect && solvedAll {
				s.store.SaveNotification(ctx, &models.Notification{
					ID:        createID("notification"),
					SeminarID: seminar.ID,
					CreatedAt: time.Now(),
					Level:     "success",
					Title:     "Семинар завершён студентом",
					Body:      fmt.Sprintf("%s закрыл(а) все задачи текущего семинара.", user.FullName),
				})
			}
		}

		execMs := outcome.ExecutionTimeMs
		s.store.AppendEvent(ctx, &models.EventLog{
			ID:              createID("event"),
			UserID:          user.ID,
			Role:            user.Role,
			SessionID:       sessionID,
			SeminarID:       strPtr(seminar.ID),
			TaskID:          strPtr(taskID),
			EventType:       fmt.Sprintf("submission.%s", outcome.Status),
			SqlText:         strPtr(sqlText),
			Status:          strPtr(string(outcome.Status)),
			ExecutionTimeMs: &execMs,
			Payload:         map[string]interface{}{"datasetsTotal": len(outcome.Details)},
			CreatedAt:       time.Now(),
		})
		return nil

	// ── playground ───────────────────────────────────────────────────
	case "run-playground-query":
		challengeID := getString(payload, "challengeId")
		sqlText := getString(payload, "sqlText")

		challenge, err := s.store.GetChallengeByID(ctx, challengeID)
		if err != nil {
			return fmt.Errorf("Challenge не найден.")
		}

		sel, _ := s.store.GetPlaygroundSelection(ctx, user.ID)
		dsID := challenge.DatasetIDs[0]
		if sel != nil && sel.SelectedPlaygroundDatasetID != "" {
			dsID = sel.SelectedPlaygroundDatasetID
		}
		ds, err := s.store.GetDataset(ctx, dsID)
		if err != nil {
			return fmt.Errorf("Датасет не найден.")
		}

		result, execMs, execErr := validation.RunQuery(ctx, s.pool, ds.InitSql, sqlText, 60)
		run := &models.QueryRun{
			ID:                    createID("run"),
			UserID:                user.ID,
			Role:                  user.Role,
			Context:               models.QueryContextPlayground,
			PlaygroundChallengeID: strPtr(challengeID),
			DatasetID:             dsID,
			SqlText:               sqlText,
			ExecutionTimeMs:       execMs,
			CreatedAt:             time.Now(),
		}
		if execErr != nil {
			run.Status = "error"
			msg := execErr.Error()
			run.ErrorMessage = &msg
		} else {
			run.Status = "success"
			run.Result = result
			if result != nil {
				run.RowCount = len(result.Rows)
			}
		}
		s.store.SaveQueryRun(ctx, run)
		return nil

	case "validate-playground-challenge":
		challengeID := getString(payload, "challengeId")
		sqlText := getString(payload, "sqlText")

		challenge, err := s.store.GetChallengeByID(ctx, challengeID)
		if err != nil {
			return fmt.Errorf("Challenge не найден.")
		}

		taskLike := challenge.AsTask(seminar.ID)
		datasets, err := s.store.GetDatasetsByIDs(ctx, challenge.DatasetIDs)
		if err != nil {
			return fmt.Errorf("Датасеты не найдены.")
		}

		outcome := validation.ValidateAgainstDatasets(ctx, s.pool, sqlText, taskLike, datasets)

		level := "info"
		if outcome.Status == models.SubmissionCorrect {
			level = "success"
		}
		s.store.SaveNotification(ctx, &models.Notification{
			ID:        createID("notification"),
			SeminarID: seminar.ID,
			CreatedAt: time.Now(),
			Level:     level,
			Title:     fmt.Sprintf("Playground: %s", challenge.Title),
			Body:      outcome.Summary,
		})

		s.store.AppendEvent(ctx, &models.EventLog{
			ID:                    createID("event"),
			UserID:                user.ID,
			Role:                  user.Role,
			SessionID:             sessionID,
			PlaygroundChallengeID: strPtr(challengeID),
			EventType:             fmt.Sprintf("playground.validation.%s", outcome.Status),
			SqlText:               strPtr(sqlText),
			Status:                strPtr(string(outcome.Status)),
			Payload:               map[string]interface{}{"summary": outcome.Summary},
			CreatedAt:             time.Now(),
		})
		return nil

	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtr(i int) *int {
	return &i
}
