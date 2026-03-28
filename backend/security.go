package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	maxAuthBodyBytes   int64 = 8 << 10
	maxActionBodyBytes int64 = 1 << 20
	maxLoginLength           = 64
	maxPasswordLength        = 256
	maxSurnameLength         = 80
	maxTitleLength           = 160
	maxDescriptionLength     = 4000
	maxAccessCodeLength      = 32
	maxDraftLength           = 20000
	maxSQLLength             = 20000
	maxImportSQLLength       = 300000
	maxTaskTypeLength        = 80
	maxConstructsLength      = 200
	maxHintsLength           = 2000
	maxGroupTitleLength      = 64
	maxGroupStreamLength     = 80
	maxDatasetCount          = 12
	maxDatasetLabelLength    = 80
	maxDatasetDescLength     = 400
	maxDatasetSeedLength     = 120000
	maxStatementsPerSQL      = 24
)

var (
	loginPattern      = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
	surnamePattern    = regexp.MustCompile(`^[\p{L}\p{M}\s'’-]{1,80}$`)
	accessCodePattern = regexp.MustCompile(`^[A-Z0-9-]{3,32}$`)
	taskTypePattern   = regexp.MustCompile(`^[\p{L}\p{M}0-9 _()/:.-]{1,80}$`)
	groupValuePattern = regexp.MustCompile(`^[\p{L}\p{M}0-9 _().:/-]{1,80}$`)
)

type rateLimitPolicy struct {
	Limit   int
	Window  time.Duration
	Lockout time.Duration
}

type rateBucket struct {
	Count       int
	WindowStart time.Time
	BlockUntil  time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[string]rateBucket{}}
}

func (r *rateLimiter) allow(key string, policy rateLimitPolicy) (bool, time.Duration) {
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	bucket := r.buckets[key]
	if !bucket.BlockUntil.IsZero() && now.Before(bucket.BlockUntil) {
		return false, time.Until(bucket.BlockUntil)
	}

	if bucket.WindowStart.IsZero() || now.Sub(bucket.WindowStart) > policy.Window {
		bucket = rateBucket{WindowStart: now}
	}

	bucket.Count++
	if bucket.Count > policy.Limit {
		if policy.Lockout > 0 {
			bucket.BlockUntil = now.Add(policy.Lockout)
		}
		r.buckets[key] = bucket
		if !bucket.BlockUntil.IsZero() {
			return false, time.Until(bucket.BlockUntil)
		}
		return false, policy.Window
	}

	r.buckets[key] = bucket
	return true, 0
}

func (r *rateLimiter) reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.buckets, key)
}

func defaultAllowedOrigins() map[string]struct{} {
	return map[string]struct{}{
		"http://localhost:3001":  {},
		"http://127.0.0.1:3001":  {},
		"http://localhost:5173":  {},
		"http://127.0.0.1:5173":  {},
		"https://localhost:3001": {},
		"https://127.0.0.1:3001": {},
	}
}

func parseAllowedOrigins(value string) map[string]struct{} {
	if strings.TrimSpace(value) == "" {
		return defaultAllowedOrigins()
	}

	result := map[string]struct{}{}
	for _, item := range strings.Split(value, ",") {
		origin := normalizeOrigin(item)
		if origin != "" {
			result[origin] = struct{}{}
		}
	}
	if len(result) == 0 {
		return defaultAllowedOrigins()
	}
	return result
}

func normalizeOrigin(value string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(value)), "/")
}

func (a *app) isAllowedOrigin(origin string) bool {
	if strings.TrimSpace(origin) == "" {
		return true
	}
	_, ok := a.allowedOrigins[normalizeOrigin(origin)]
	return ok
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwardedProto != "" {
		scheme = forwardedProto
	}
	return normalizeOrigin(fmt.Sprintf("%s://%s", scheme, r.Host))
}

func (a *app) isAllowedRequestOrigin(r *http.Request) bool {
	origin := normalizeOrigin(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	return origin == requestOrigin(r) || a.isAllowedOrigin(origin)
}

func (a *app) setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' ws: wss:; font-src 'self' data:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		if ip := net.ParseIP(forwarded); ip != nil {
			return ip.String()
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-Ip")); realIP != "" {
		if ip := net.ParseIP(realIP); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dest any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		var syntaxError *json.SyntaxError
		switch {
		case errors.Is(err, io.EOF):
			return io.EOF
		case errors.As(err, &syntaxError):
			return errors.New("Некорректный JSON.")
		case strings.Contains(err.Error(), "http: request body too large"):
			return fmt.Errorf("Тело запроса превышает ограничение %d байт.", limit)
		default:
			return errors.New("Некорректное тело запроса.")
		}
	}

	if decoder.More() {
		return errors.New("Тело запроса должно содержать один JSON-объект.")
	}
	return nil
}

func sanitizeText(value string, maxLen int, multiline bool) string {
	value = strings.ReplaceAll(value, "\x00", "")
	if !multiline {
		value = strings.ReplaceAll(value, "\r", " ")
		value = strings.ReplaceAll(value, "\n", " ")
	}
	value = strings.Map(func(r rune) rune {
		if r == '\n' && multiline {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > maxLen {
		runes := []rune(value)
		value = string(runes[:maxLen])
	}
	return value
}

const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

func hashPassword(value string) string {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	hash := argon2.IDKey([]byte(value), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}

func hashPasswordLegacy(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func verifyPasswordHash(value, storedHash string) (bool, bool) {
	if strings.HasPrefix(storedHash, "argon2id$") {
		parts := strings.Split(storedHash, "$")
		if len(parts) != 5 {
			return false, false
		}
		if parts[1] != "v=19" {
			return false, false
		}
		var memory uint32
		var timeCost uint32
		var threads uint8
		if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
			return false, false
		}
		salt, err := base64.RawStdEncoding.DecodeString(parts[3])
		if err != nil {
			return false, false
		}
		expected, err := base64.RawStdEncoding.DecodeString(parts[4])
		if err != nil {
			return false, false
		}
		actual := argon2.IDKey([]byte(value), salt, timeCost, memory, threads, uint32(len(expected)))
		return subtle.ConstantTimeCompare(actual, expected) == 1, false
	}

	legacyHash := hashPasswordLegacy(value)
	if subtle.ConstantTimeCompare([]byte(legacyHash), []byte(storedHash)) == 1 {
		return true, true
	}
	return false, false
}

func validateRequiredText(label, value string, maxLen int, multiline bool) (string, error) {
	sanitized := sanitizeText(value, maxLen, multiline)
	if sanitized == "" {
		return "", fmt.Errorf("Поле \"%s\" не может быть пустым.", label)
	}
	return sanitized, nil
}

func validateOptionalText(value string, maxLen int, multiline bool) string {
	return sanitizeText(value, maxLen, multiline)
}

func validateLoginValue(value string) (string, error) {
	sanitized := sanitizeText(value, maxLoginLength, false)
	if !loginPattern.MatchString(sanitized) {
		return "", errors.New("Некорректный логин.")
	}
	return sanitized, nil
}

func validatePasswordValue(value string) (string, error) {
	sanitized := sanitizeText(value, maxPasswordLength, false)
	if sanitized == "" {
		return "", errors.New("Пароль не может быть пустым.")
	}
	return sanitized, nil
}

func validateSurnameValue(value string) (string, error) {
	sanitized := sanitizeText(value, maxSurnameLength, false)
	if !surnamePattern.MatchString(sanitized) {
		return "", errors.New("Введите корректную фамилию.")
	}
	return sanitized, nil
}

func validateAccessCodeValue(value string) (string, error) {
	sanitized := strings.ToUpper(sanitizeText(value, maxAccessCodeLength, false))
	if !accessCodePattern.MatchString(sanitized) {
		return "", errors.New("Код доступа должен содержать только латинские буквы, цифры и дефис.")
	}
	return sanitized, nil
}

func validateTaskTypeValue(value string) (string, error) {
	sanitized := sanitizeText(value, maxTaskTypeLength, false)
	if !taskTypePattern.MatchString(sanitized) {
		return "", errors.New("Некорректный тип задачи.")
	}
	return sanitized, nil
}

func validateDifficultyValue(value string) (string, error) {
	switch sanitizeText(value, 16, false) {
	case "easy", "medium", "hard":
		return sanitizeText(value, 16, false), nil
	default:
		return "", errors.New("Некорректный уровень сложности.")
	}
}

func validateGroupTitleValue(value string) (string, error) {
	sanitized := sanitizeText(value, maxGroupTitleLength, false)
	if !groupValuePattern.MatchString(sanitized) {
		return "", errors.New("Некорректное название группы.")
	}
	return sanitized, nil
}

func validateGroupStreamValue(value string) (string, error) {
	sanitized := sanitizeText(value, maxGroupStreamLength, false)
	if !groupValuePattern.MatchString(sanitized) {
		return "", errors.New("Некорректное направление группы.")
	}
	return sanitized, nil
}

func validateDateTimeValue(label, value string) (string, error) {
	normalized := normaliseDateValue(value)
	parsed, err := time.Parse(time.RFC3339, normalized)
	if err != nil {
		return "", fmt.Errorf("Поле \"%s\" содержит некорректную дату.", label)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func validateSQLValue(label, value string, maxLen int, allowEmpty bool) (string, error) {
	sanitized := sanitizeText(value, maxLen, true)
	if sanitized == "" && !allowEmpty {
		return "", fmt.Errorf("Поле \"%s\" не может быть пустым.", label)
	}
	if sanitized == "" {
		return "", nil
	}
	if len(splitSQLStatements(sanitized)) > maxStatementsPerSQL {
		return "", fmt.Errorf("В поле \"%s\" слишком много SQL-операторов.", label)
	}
	return sanitized, nil
}

func validateImportDatasets(inputs []importDatasetInput) ([]importDatasetInput, error) {
	if len(inputs) > maxDatasetCount {
		return nil, fmt.Errorf("Количество датасетов не должно превышать %d.", maxDatasetCount)
	}

	if len(inputs) == 0 {
		return []importDatasetInput{}, nil
	}

	result := make([]importDatasetInput, 0, len(inputs))
	for index, dataset := range inputs {
		label, err := validateRequiredText(fmt.Sprintf("Название датасета %d", index+1), dataset.Label, maxDatasetLabelLength, false)
		if err != nil {
			return nil, err
		}
		description := validateOptionalText(dataset.Description, maxDatasetDescLength, true)
		seedSQL, err := validateSQLValue(fmt.Sprintf("SQL датасета %d", index+1), dataset.SeedSQL, maxDatasetSeedLength, true)
		if err != nil {
			return nil, err
		}
		result = append(result, importDatasetInput{
			Label:       label,
			Description: description,
			SeedSQL:     seedSQL,
		})
	}
	return result, nil
}

func validateIDList(raw []string, maxCount int) ([]string, error) {
	if len(raw) > maxCount {
		return nil, errors.New("Слишком много элементов в списке.")
	}
	result := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, value := range raw {
		id := sanitizeText(value, 80, false)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func validateActionRateLimit(action string) rateLimitPolicy {
	switch action {
	case "run-seminar-query", "run-playground-query":
		return rateLimitPolicy{Limit: 40, Window: time.Minute, Lockout: 2 * time.Minute}
	case "submit-seminar-query", "validate-playground-challenge":
		return rateLimitPolicy{Limit: 20, Window: time.Minute, Lockout: 3 * time.Minute}
	case "import-template", "reset":
		return rateLimitPolicy{Limit: 4, Window: 10 * time.Minute, Lockout: 15 * time.Minute}
	default:
		return rateLimitPolicy{Limit: 120, Window: time.Minute, Lockout: 2 * time.Minute}
	}
}

func writeRateLimitError(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
	writeError(w, http.StatusTooManyRequests, "Слишком много запросов. Повторите позже.")
}

func truncateForLog(value string, maxLen int) string {
	value = sanitizeText(value, maxLen, true)
	if utf8.RuneCountInString(value) <= maxLen {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxLen])
}
