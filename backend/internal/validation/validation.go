package validation

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sql-seminar/backend/internal/models"
)

type Outcome struct {
	Status          models.SubmissionStatus
	Summary         string
	Details         []models.DatasetValidationOutcome
	ExecutionTimeMs int
}

func findForbiddenKeyword(sqlText string, keywords []string) string {
	for _, kw := range keywords {
		pattern := `(?i)\b` + regexp.QuoteMeta(kw) + `\b`
		if matched, _ := regexp.MatchString(pattern, sqlText); matched {
			return kw
		}
	}
	return ""
}

func normaliseString(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}

func normaliseCell(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case float64:
		rounded, _ := fmt.Sscanf(fmt.Sprintf("%.6f", val), "%f", &val)
		_ = rounded
		return math.Round(val*1e6) / 1e6
	case string:
		return normaliseString(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func compareCells(actual, expected interface{}, tolerance float64) bool {
	if actual == nil && expected == nil {
		return true
	}
	if actual == nil || expected == nil {
		return false
	}
	// numeric comparison
	aFloat, aIsFloat := toFloat64(actual)
	eFloat, eIsFloat := toFloat64(expected)
	if aIsFloat && eIsFloat {
		return math.Abs(aFloat-eFloat) <= tolerance
	}
	return fmt.Sprintf("%v", normaliseCell(actual)) == fmt.Sprintf("%v", normaliseCell(expected))
}

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	case int:
		return float64(val), true
	}
	return 0, false
}

func compareResults(actual, expected *models.QueryResultTable, cfg models.ValidationConfig) string {
	if cfg.ColumnNamesMatter {
		if len(actual.Columns) != len(expected.Columns) {
			return "Количество столбцов отличается от эталона."
		}
		for i := range actual.Columns {
			if normaliseString(actual.Columns[i]) != normaliseString(expected.Columns[i]) {
				return fmt.Sprintf("Имя столбца %d не совпадает с эталоном.", i+1)
			}
		}
	}

	actualRows := actual.Rows
	expectedRows := expected.Rows
	if !cfg.OrderMatters {
		actualRows = sortRows(actual.Rows)
		expectedRows = sortRows(expected.Rows)
	}

	if len(actualRows) != len(expectedRows) {
		return "Количество строк отличается от эталона."
	}
	for ri, eRow := range expectedRows {
		aRow := actualRows[ri]
		if len(aRow) != len(eRow) {
			return fmt.Sprintf("Строка %d содержит другое количество значений.", ri+1)
		}
		for ci := range eRow {
			if !compareCells(aRow[ci], eRow[ci], cfg.NumericTolerance) {
				return fmt.Sprintf("Строка %d, столбец %d отличается от эталона.", ri+1, ci+1)
			}
		}
	}
	return ""
}

func sortRows(rows [][]interface{}) [][]interface{} {
	sorted := make([][]interface{}, len(rows))
	copy(sorted, rows)
	// simple bubble sort for determinism (datasets are small)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if rowKey(sorted[i]) > rowKey(sorted[j]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

func rowKey(row []interface{}) string {
	parts := make([]string, len(row))
	for i, v := range row {
		parts[i] = fmt.Sprintf("%v", normaliseCell(v))
	}
	return strings.Join(parts, "|")
}

// execInTempDB runs sqlText against a fresh temporary PostgreSQL schema
// created by running initSql, then tears it down.
func execInTempDB(ctx context.Context, pool *pgxpool.Pool, initSql, sqlText string, maxRows int) (*models.QueryResultTable, int, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	// Use a unique schema per execution to isolate datasets
	schemaName := fmt.Sprintf("sandbox_%d", time.Now().UnixNano())

	_, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	if err != nil {
		return nil, 0, fmt.Errorf("create sandbox schema: %w", err)
	}

	defer func() {
		dropCtx := context.Background()
		conn.Exec(dropCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName)) //nolint
	}()

	// set search_path so tables go into sandbox schema
	_, err = conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", schemaName))
	if err != nil {
		return nil, 0, fmt.Errorf("set search_path: %w", err)
	}

	// initialise dataset
	_, err = conn.Exec(ctx, initSql)
	if err != nil {
		return nil, 0, fmt.Errorf("init dataset: %w", err)
	}

	start := time.Now()
	rows, err := conn.Query(ctx, sqlText)
	elapsed := int(time.Since(start).Milliseconds())
	if err != nil {
		return nil, elapsed, fmt.Errorf("query error: %w", err)
	}
	defer rows.Close()

	fieldDescs := rows.FieldDescriptions()
	columns := make([]string, len(fieldDescs))
	for i, fd := range fieldDescs {
		columns[i] = string(fd.Name)
	}

	var resultRows [][]interface{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, elapsed, fmt.Errorf("scan row: %w", err)
		}
		if maxRows > 0 && len(resultRows) >= maxRows {
			break
		}
		resultRows = append(resultRows, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, elapsed, fmt.Errorf("rows iteration: %w", err)
	}

	return &models.QueryResultTable{Columns: columns, Rows: resultRows}, elapsed, nil
}

// execDDLInTempDB runs DDL (CREATE VIEW/TRIGGER/INDEX) and then an optional
// query in the sandbox schema, returning the query result.
func execDDLInTempDB(ctx context.Context, pool *pgxpool.Pool, initSql, ddlSql string, simSqls []string, querySql string) (*models.QueryResultTable, int, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	schemaName := fmt.Sprintf("sandbox_%d", time.Now().UnixNano())
	if _, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName)); err != nil {
		return nil, 0, fmt.Errorf("create sandbox schema: %w", err)
	}
	defer conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName)) //nolint

	if _, err = conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", schemaName)); err != nil {
		return nil, 0, err
	}
	if _, err = conn.Exec(ctx, initSql); err != nil {
		return nil, 0, fmt.Errorf("init dataset: %w", err)
	}

	start := time.Now()
	if _, err = conn.Exec(ctx, ddlSql); err != nil {
		return nil, int(time.Since(start).Milliseconds()), fmt.Errorf("ddl error: %w", err)
	}
	elapsed := int(time.Since(start).Milliseconds())

	for _, sim := range simSqls {
		if _, err = conn.Exec(ctx, sim); err != nil {
			return nil, elapsed, fmt.Errorf("simulation error: %w", err)
		}
	}

	if querySql == "" {
		return nil, elapsed, nil
	}

	rows, err := conn.Query(ctx, querySql)
	if err != nil {
		return nil, elapsed, fmt.Errorf("verification query: %w", err)
	}
	defer rows.Close()

	fds := rows.FieldDescriptions()
	cols := make([]string, len(fds))
	for i, fd := range fds {
		cols[i] = string(fd.Name)
	}
	var resRows [][]interface{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, elapsed, err
		}
		resRows = append(resRows, vals)
	}
	return &models.QueryResultTable{Columns: cols, Rows: resRows}, elapsed, nil
}

// checkObjectExists checks information_schema for a view/function/trigger existence.
// For PostgreSQL we use information_schema or pg_class depending on type.
func checkObjectExists(ctx context.Context, pool *pgxpool.Pool, initSql, ddlSql, schemaName, objectType, objectName string) (bool, int, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, 0, err
	}
	defer conn.Release()

	if _, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)); err != nil {
		return false, 0, err
	}
	defer conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName)) //nolint

	if _, err = conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", schemaName)); err != nil {
		return false, 0, err
	}
	if _, err = conn.Exec(ctx, initSql); err != nil {
		return false, 0, fmt.Errorf("init: %w", err)
	}

	start := time.Now()
	if _, err = conn.Exec(ctx, ddlSql); err != nil {
		return false, int(time.Since(start).Milliseconds()), fmt.Errorf("ddl: %w", err)
	}
	elapsed := int(time.Since(start).Milliseconds())

	// Check existence depending on type
	var exists bool
	switch strings.ToLower(objectType) {
	case "view":
		err = conn.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM information_schema.views WHERE table_schema=$1 AND table_name=$2)",
			schemaName, objectName,
		).Scan(&exists)
	case "trigger":
		err = conn.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM information_schema.triggers WHERE trigger_schema=$1 AND trigger_name=$2)",
			schemaName, objectName,
		).Scan(&exists)
	case "index":
		err = conn.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=$1 AND indexname=$2)",
			schemaName, objectName,
		).Scan(&exists)
	default:
		return false, elapsed, fmt.Errorf("unknown object type: %s", objectType)
	}

	return exists, elapsed, err
}

// ValidateAgainstDatasets runs the full validation pipeline.
func ValidateAgainstDatasets(
	ctx context.Context,
	pool *pgxpool.Pool,
	sqlText string,
	task *models.TaskDefinition,
	datasets []models.TemplateDataset,
) Outcome {
	// forbidden keywords check
	if kw := findForbiddenKeyword(sqlText, task.ValidationConfig.ForbiddenKeywords); kw != "" {
		return Outcome{
			Status:  models.SubmissionBlocked,
			Summary: fmt.Sprintf("Конструкция %s запрещена для этой задачи.", kw),
		}
	}

	switch task.ValidationMode {
	case models.ValidationDdlObject:
		return validateDDL(ctx, pool, sqlText, task, datasets)
	case models.ValidationExplainPlan:
		return validateExplain(ctx, pool, sqlText, task, datasets)
	default:
		return validateResultMatch(ctx, pool, sqlText, task, datasets)
	}
}

func validateResultMatch(ctx context.Context, pool *pgxpool.Pool, sqlText string, task *models.TaskDefinition, datasets []models.TemplateDataset) Outcome {
	var details []models.DatasetValidationOutcome
	totalMs := 0

	for _, ds := range datasets {
		timeout := time.Duration(task.ValidationConfig.MaxExecutionMs) * time.Millisecond
		qCtx, cancel := context.WithTimeout(ctx, timeout)

		actual, actualMs, err := execInTempDB(qCtx, pool, ds.InitSql, sqlText, task.ValidationConfig.MaxResultRows)
		cancel()

		totalMs += actualMs

		if err != nil {
			blocked := strings.Contains(err.Error(), "запрещ") || strings.Contains(strings.ToLower(err.Error()), "permission")
			status := models.SubmissionRuntimeError
			if blocked {
				status = models.SubmissionBlocked
			}
			return Outcome{
				Status:          status,
				Summary:         fmt.Sprintf("Ошибка выполнения на %s: %s", ds.Label, err.Error()),
				Details:         append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: false, Message: err.Error(), ExecutionTimeMs: actualMs}),
				ExecutionTimeMs: totalMs,
			}
		}

		if actualMs > task.ValidationConfig.MaxExecutionMs {
			return Outcome{
				Status:          models.SubmissionIncorrect,
				Summary:         fmt.Sprintf("Превышено ограничение %d мс на %s.", task.ValidationConfig.MaxExecutionMs, ds.Label),
				Details:         append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: false, Message: fmt.Sprintf("Запрос выполнялся %d мс.", actualMs), Actual: actual, ExecutionTimeMs: actualMs}),
				ExecutionTimeMs: totalMs,
			}
		}

		expected, _, expErr := execInTempDB(ctx, pool, ds.InitSql, task.ExpectedQuery, task.ValidationConfig.MaxResultRows)
		if expErr != nil {
			return Outcome{
				Status:          models.SubmissionRuntimeError,
				Summary:         "Системная ошибка при построении эталонного результата.",
				Details:         details,
				ExecutionTimeMs: totalMs,
			}
		}

		mismatch := compareResults(actual, expected, task.ValidationConfig)
		details = append(details, models.DatasetValidationOutcome{
			DatasetID:       ds.ID,
			Passed:          mismatch == "",
			Message:         ternaryStr(mismatch == "", fmt.Sprintf("%s: результат совпал с эталоном.", ds.Label), mismatch),
			Actual:          actual,
			Expected:        expected,
			ExecutionTimeMs: actualMs,
		})
		if mismatch != "" {
			return Outcome{
				Status:          models.SubmissionIncorrect,
				Summary:         fmt.Sprintf("Не пройден скрытый тест %s.", ds.Label),
				Details:         details,
				ExecutionTimeMs: totalMs,
			}
		}
	}
	return Outcome{
		Status:          models.SubmissionCorrect,
		Summary:         fmt.Sprintf("%d / %d датасетов пройдено.", len(details), len(details)),
		Details:         details,
		ExecutionTimeMs: totalMs,
	}
}

func validateDDL(ctx context.Context, pool *pgxpool.Pool, sqlText string, task *models.TaskDefinition, datasets []models.TemplateDataset) Outcome {
	spec := task.ValidationSpec
	if spec == nil || spec.DDL == nil {
		return Outcome{Status: models.SubmissionRuntimeError, Summary: "Для DDL-задачи не задана спецификация проверки."}
	}
	ddl := spec.DDL

	var details []models.DatasetValidationOutcome
	totalMs := 0

	for _, ds := range datasets {
		schemaName := fmt.Sprintf("sandbox_%d", time.Now().UnixNano())

		conn, err := pool.Acquire(ctx)
		if err != nil {
			return Outcome{Status: models.SubmissionRuntimeError, Summary: err.Error()}
		}

		cleanup := func() {
			conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName)) //nolint
			conn.Release()
		}

		if _, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName)); err != nil {
			cleanup()
			return Outcome{Status: models.SubmissionRuntimeError, Summary: err.Error()}
		}
		if _, err = conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", schemaName)); err != nil {
			cleanup()
			return Outcome{Status: models.SubmissionRuntimeError, Summary: err.Error()}
		}
		if _, err = conn.Exec(ctx, ds.InitSql); err != nil {
			cleanup()
			return Outcome{Status: models.SubmissionRuntimeError, Summary: fmt.Sprintf("init dataset: %v", err)}
		}

		start := time.Now()
		if _, err = conn.Exec(ctx, sqlText); err != nil {
			elapsed := int(time.Since(start).Milliseconds())
			cleanup()
			return Outcome{
				Status:          models.SubmissionRuntimeError,
				Summary:         err.Error(),
				Details:         append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: false, Message: err.Error(), ExecutionTimeMs: elapsed}),
				ExecutionTimeMs: totalMs + elapsed,
			}
		}
		elapsed := int(time.Since(start).Milliseconds())
		totalMs += elapsed

		// Check object exists
		var exists bool
		switch strings.ToLower(ddl.ObjectType) {
		case "view":
			conn.QueryRow(ctx,
				"SELECT EXISTS(SELECT 1 FROM information_schema.views WHERE table_schema=$1 AND table_name=$2)",
				schemaName, ddl.ObjectName).Scan(&exists) //nolint
		case "trigger":
			conn.QueryRow(ctx,
				"SELECT EXISTS(SELECT 1 FROM information_schema.triggers WHERE trigger_schema=$1 AND trigger_name=$2)",
				schemaName, ddl.ObjectName).Scan(&exists) //nolint
		case "index":
			conn.QueryRow(ctx,
				"SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=$1 AND indexname=$2)",
				schemaName, ddl.ObjectName).Scan(&exists) //nolint
		}

		if !exists {
			msg := fmt.Sprintf("%s: объект %s %s не создан.", ds.Label, ddl.ObjectType, ddl.ObjectName)
			details = append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: false, Message: msg, ExecutionTimeMs: elapsed})
			cleanup()
			return Outcome{Status: models.SubmissionIncorrect, Summary: fmt.Sprintf("На %s ожидаемый объект не найден.", ds.Label), Details: details, ExecutionTimeMs: totalMs}
		}

		// Run simulation SQL
		for _, sim := range ddl.SimulationSql {
			if _, err = conn.Exec(ctx, sim); err != nil {
				cleanup()
				return Outcome{Status: models.SubmissionRuntimeError, Summary: fmt.Sprintf("simulation error: %v", err), Details: details, ExecutionTimeMs: totalMs}
			}
		}

		// Verification query
		if ddl.VerificationQuery != "" && ddl.VerificationExpectedQuery != "" {
			actual, err := queryRows(ctx, conn, ddl.VerificationQuery)
			if err != nil {
				cleanup()
				return Outcome{Status: models.SubmissionRuntimeError, Summary: fmt.Sprintf("verification query error: %v", err), Details: details, ExecutionTimeMs: totalMs}
			}

			// Run expected in same schema
			expected, err := queryRows(ctx, conn, ddl.VerificationExpectedQuery)
			if err != nil {
				cleanup()
				return Outcome{Status: models.SubmissionRuntimeError, Summary: "Ошибка при получении эталонного результата верификации.", Details: details, ExecutionTimeMs: totalMs}
			}

			mismatch := compareResults(actual, expected, task.ValidationConfig)
			if mismatch != "" {
				details = append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: false, Message: fmt.Sprintf("%s: %s", ds.Label, mismatch), Actual: actual, Expected: expected, ExecutionTimeMs: elapsed})
				cleanup()
				return Outcome{Status: models.SubmissionIncorrect, Summary: fmt.Sprintf("Контрольный сценарий для %s не пройден.", ddl.ObjectType), Details: details, ExecutionTimeMs: totalMs}
			}
			details = append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: true, Message: fmt.Sprintf("%s: объект создан и контрольный сценарий пройден.", ds.Label), Actual: actual, Expected: expected, ExecutionTimeMs: elapsed})
		} else {
			details = append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: true, Message: fmt.Sprintf("%s: объект %s %s создан.", ds.Label, ddl.ObjectType, ddl.ObjectName), ExecutionTimeMs: elapsed})
		}

		cleanup()
	}

	return Outcome{
		Status:          models.SubmissionCorrect,
		Summary:         fmt.Sprintf("%d / %d датасетов пройдено для DDL-проверки.", len(details), len(details)),
		Details:         details,
		ExecutionTimeMs: totalMs,
	}
}

func validateExplain(ctx context.Context, pool *pgxpool.Pool, sqlText string, task *models.TaskDefinition, datasets []models.TemplateDataset) Outcome {
	spec := task.ValidationSpec
	if spec == nil || spec.Explain == nil {
		return Outcome{Status: models.SubmissionRuntimeError, Summary: "Для EXPLAIN-задачи не задана спецификация проверки."}
	}
	expl := spec.Explain

	var details []models.DatasetValidationOutcome
	totalMs := 0

	for _, ds := range datasets {
		schemaName := fmt.Sprintf("sandbox_%d", time.Now().UnixNano())

		conn, err := pool.Acquire(ctx)
		if err != nil {
			return Outcome{Status: models.SubmissionRuntimeError, Summary: err.Error()}
		}
		cleanup := func() {
			conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName)) //nolint
			conn.Release()
		}

		if _, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName)); err != nil {
			cleanup()
			return Outcome{Status: models.SubmissionRuntimeError, Summary: err.Error()}
		}
		if _, err = conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s", schemaName)); err != nil {
			cleanup()
			return Outcome{Status: models.SubmissionRuntimeError, Summary: err.Error()}
		}
		if _, err = conn.Exec(ctx, ds.InitSql); err != nil {
			cleanup()
			return Outcome{Status: models.SubmissionRuntimeError, Summary: fmt.Sprintf("init: %v", err)}
		}

		start := time.Now()
		if _, err = conn.Exec(ctx, sqlText); err != nil {
			elapsed := int(time.Since(start).Milliseconds())
			cleanup()
			return Outcome{
				Status:          models.SubmissionRuntimeError,
				Summary:         err.Error(),
				Details:         append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: false, Message: err.Error(), ExecutionTimeMs: elapsed}),
				ExecutionTimeMs: totalMs + elapsed,
			}
		}
		elapsed := int(time.Since(start).Milliseconds())
		totalMs += elapsed

		// Check DDL object if specified
		if spec.DDL != nil {
			var exists bool
			switch strings.ToLower(spec.DDL.ObjectType) {
			case "index":
				conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=$1 AND indexname=$2)", schemaName, spec.DDL.ObjectName).Scan(&exists) //nolint
			}
			if !exists {
				msg := fmt.Sprintf("%s: индекс %s не найден.", ds.Label, spec.DDL.ObjectName)
				details = append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: false, Message: msg, ExecutionTimeMs: elapsed})
				cleanup()
				return Outcome{Status: models.SubmissionIncorrect, Summary: "Ожидаемый индекс не создан.", Details: details, ExecutionTimeMs: totalMs}
			}
		}

		// EXPLAIN ANALYZE
		planRows, err := conn.Query(ctx, "EXPLAIN "+expl.TargetSql)
		if err != nil {
			cleanup()
			return Outcome{Status: models.SubmissionRuntimeError, Summary: fmt.Sprintf("EXPLAIN error: %v", err), Details: details, ExecutionTimeMs: totalMs}
		}
		var planParts []string
		for planRows.Next() {
			var line string
			if err := planRows.Scan(&line); err == nil {
				planParts = append(planParts, strings.ToLower(line))
			}
		}
		planRows.Close()
		planText := strings.Join(planParts, " ")

		var planTable models.QueryResultTable
		planTable.Columns = []string{"QUERY PLAN"}
		for _, p := range planParts {
			planTable.Rows = append(planTable.Rows, []interface{}{p})
		}

		for _, kw := range expl.RequiredPlanKeywords {
			if !strings.Contains(planText, strings.ToLower(kw)) {
				msg := fmt.Sprintf("%s: план не содержит обязательный фрагмент %s.", ds.Label, kw)
				details = append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: false, Message: msg, Actual: &planTable, ExecutionTimeMs: elapsed})
				cleanup()
				return Outcome{Status: models.SubmissionIncorrect, Summary: "План выполнения не соответствует ожиданиям.", Details: details, ExecutionTimeMs: totalMs}
			}
		}

		details = append(details, models.DatasetValidationOutcome{DatasetID: ds.ID, Passed: true, Message: fmt.Sprintf("%s: план выполнения содержит ожидаемые признаки.", ds.Label), Actual: &planTable, ExecutionTimeMs: elapsed})
		cleanup()
	}

	return Outcome{
		Status:          models.SubmissionCorrect,
		Summary:         fmt.Sprintf("%d / %d датасетов пройдено для EXPLAIN-проверки.", len(details), len(details)),
		Details:         details,
		ExecutionTimeMs: totalMs,
	}
}

// RunQuery executes a single SQL query in a sandbox and returns result (for "run" actions).
func RunQuery(ctx context.Context, pool *pgxpool.Pool, initSql, sqlText string, maxRows int) (*models.QueryResultTable, int, error) {
	return execInTempDB(ctx, pool, initSql, sqlText, maxRows)
}

func queryRows(ctx context.Context, conn *pgxpool.Conn, sql string) (*models.QueryResultTable, error) {
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fds := rows.FieldDescriptions()
	cols := make([]string, len(fds))
	for i, fd := range fds {
		cols[i] = string(fd.Name)
	}
	var res [][]interface{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		res = append(res, vals)
	}
	return &models.QueryResultTable{Columns: cols, Rows: res}, rows.Err()
}

func ternaryStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// Ensure pgx import used
var _ = pgx.ErrNoRows
