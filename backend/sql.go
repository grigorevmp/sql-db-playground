package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var globalForbiddenSQLKeywords = []string{
	"ATTACH",
	"DETACH",
	"PRAGMA",
	"VACUUM",
	"ANALYZE",
	"REINDEX",
	"LOAD_EXTENSION",
}

const sqlExecutionTimeout = 5 * time.Second

type ExecutionResult struct {
	OK              bool
	Result          *QueryResultTable
	ExecutionTimeMs int
	RowCount        int
	ErrorMessage    string
}

func newSQLiteDB() (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=private", createID("db"))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Minute)
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 1000;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA trusted_schema = OFF;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func splitSQLStatements(input string) []string {
	var statements []string
	var builder strings.Builder
	inSingle := false
	inDouble := false

	for _, r := range input {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ';':
			if !inSingle && !inDouble {
				statement := strings.TrimSpace(builder.String())
				if statement != "" {
					statements = append(statements, statement)
				}
				builder.Reset()
				continue
			}
		}
		builder.WriteRune(r)
	}

	if builder.Len() > 0 {
		statement := strings.TrimSpace(builder.String())
		if statement != "" && (len(statements) == 0 || statements[len(statements)-1] != statement) {
			statements = append(statements, statement)
		}
	}

	return statements
}

func isQueryStatement(statement string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(statement))
	return strings.HasPrefix(trimmed, "select") ||
		strings.HasPrefix(trimmed, "with") ||
		strings.HasPrefix(trimmed, "explain") ||
		strings.HasPrefix(trimmed, "pragma")
}

func normaliseCell(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case int64:
		return float64(typed)
	case int:
		return float64(typed)
	case float64:
		return math.Round(typed*1e6) / 1e6
	case float32:
		return math.Round(float64(typed)*1e6) / 1e6
	case []byte:
		return strings.ToLower(strings.Join(strings.Fields(string(typed)), " "))
	case string:
		return strings.ToLower(strings.Join(strings.Fields(typed), " "))
	default:
		return fmt.Sprint(typed)
	}
}

func compareCells(actual any, expected any, config ValidationConfig) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}

	actualNumber, actualIsNumber := toFloat(actual)
	expectedNumber, expectedIsNumber := toFloat(expected)
	if actualIsNumber && expectedIsNumber {
		return math.Abs(actualNumber-expectedNumber) <= config.NumericTolerance
	}

	return normaliseCell(actual) == normaliseCell(expected)
}

func compareResults(actual QueryResultTable, expected QueryResultTable, config ValidationConfig) string {
	if config.ColumnNamesMatter {
		if len(actual.Columns) != len(expected.Columns) {
			return "Количество столбцов отличается от эталона."
		}
		for idx := range actual.Columns {
			if normaliseCell(actual.Columns[idx]) != normaliseCell(expected.Columns[idx]) {
				return fmt.Sprintf("Имя столбца %d не совпадает с эталоном.", idx+1)
			}
		}
	}

	actualRows := actual.Rows
	expectedRows := expected.Rows
	if !config.OrderMatters {
		actualRows = sortRows(actual.Rows)
		expectedRows = sortRows(expected.Rows)
	}

	if len(actualRows) != len(expectedRows) {
		return "Количество строк отличается от эталона."
	}

	for rowIndex := range expectedRows {
		if len(actualRows[rowIndex]) != len(expectedRows[rowIndex]) {
			return fmt.Sprintf("Строка %d содержит другое количество значений.", rowIndex+1)
		}
		for columnIndex := range expectedRows[rowIndex] {
			if !compareCells(actualRows[rowIndex][columnIndex], expectedRows[rowIndex][columnIndex], config) {
				return fmt.Sprintf("Строка %d, столбец %d отличается от эталона.", rowIndex+1, columnIndex+1)
			}
		}
	}

	return ""
}

func sortRows(rows [][]any) [][]any {
	cloned := make([][]any, len(rows))
	copy(cloned, rows)
	for i := range cloned {
		for j := i + 1; j < len(cloned); j++ {
			left := flattenRow(cloned[i])
			right := flattenRow(cloned[j])
			if left > right {
				cloned[i], cloned[j] = cloned[j], cloned[i]
			}
		}
	}
	return cloned
}

func flattenRow(row []any) string {
	parts := make([]string, 0, len(row))
	for _, cell := range row {
		parts = append(parts, fmt.Sprint(normaliseCell(cell)))
	}
	return strings.Join(parts, "|")
}

func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	default:
		return 0, false
	}
}

func findForbiddenKeyword(sqlText string, forbiddenKeywords []string) string {
	for _, keyword := range forbiddenKeywords {
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(keyword) + `\b`)
		if pattern.MatchString(sqlText) {
			return keyword
		}
	}
	return ""
}

func executeSQL(initSQL string, sqlText string, maxResultRows int) ExecutionResult {
	blockedKeyword := findForbiddenKeyword(sqlText, globalForbiddenSQLKeywords)
	if blockedKeyword != "" {
		return ExecutionResult{OK: false, ErrorMessage: fmt.Sprintf("Конструкция %s запрещена для выполнения.", blockedKeyword)}
	}
	if len(splitSQLStatements(sqlText)) > maxStatementsPerSQL {
		return ExecutionResult{OK: false, ErrorMessage: "Слишком много SQL-операторов в одном запросе."}
	}

	db, err := newSQLiteDB()
	if err != nil {
		return ExecutionResult{OK: false, ErrorMessage: err.Error()}
	}
	defer db.Close()

	if err := executeStatements(db, initSQL); err != nil {
		return ExecutionResult{OK: false, ErrorMessage: err.Error()}
	}

	startedAt := time.Now()
	result, rowCount, err := executeUserStatements(db, sqlText, maxResultRows)
	executionTimeMs := int(time.Since(startedAt).Milliseconds())
	if err != nil {
		return ExecutionResult{OK: false, ErrorMessage: err.Error(), ExecutionTimeMs: executionTimeMs}
	}

	return ExecutionResult{
		OK:              true,
		Result:          result,
		ExecutionTimeMs: executionTimeMs,
		RowCount:        rowCount,
	}
}

func executeStatements(db *sql.DB, sqlText string) error {
	for _, statement := range splitSQLStatements(sqlText) {
		if blockedKeyword := findForbiddenKeyword(statement, globalForbiddenSQLKeywords); blockedKeyword != "" {
			return fmt.Errorf("Конструкция %s запрещена для выполнения.", blockedKeyword)
		}
		ctx, cancel := context.WithTimeout(context.Background(), sqlExecutionTimeout)
		_, err := db.ExecContext(ctx, statement)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func executeUserStatements(db *sql.DB, sqlText string, maxResultRows int) (*QueryResultTable, int, error) {
	statements := splitSQLStatements(sqlText)
	if len(statements) == 0 {
		return nil, 0, errors.New("SQL-запрос пуст.")
	}

	for _, statement := range statements[:len(statements)-1] {
		if blockedKeyword := findForbiddenKeyword(statement, globalForbiddenSQLKeywords); blockedKeyword != "" {
			return nil, 0, fmt.Errorf("Конструкция %s запрещена для выполнения.", blockedKeyword)
		}
		ctx, cancel := context.WithTimeout(context.Background(), sqlExecutionTimeout)
		_, err := db.ExecContext(ctx, statement)
		cancel()
		if err != nil {
			return nil, 0, err
		}
	}

	lastStatement := statements[len(statements)-1]
	if isQueryStatement(lastStatement) {
		result, rowCount, err := queryToTable(db, lastStatement, maxResultRows)
		return result, rowCount, err
	}

	if blockedKeyword := findForbiddenKeyword(lastStatement, globalForbiddenSQLKeywords); blockedKeyword != "" {
		return nil, 0, fmt.Errorf("Конструкция %s запрещена для выполнения.", blockedKeyword)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqlExecutionTimeout)
	defer cancel()
	execResult, err := db.ExecContext(ctx, lastStatement)
	if err != nil {
		return nil, 0, err
	}
	rowsAffected, _ := execResult.RowsAffected()
	return nil, int(rowsAffected), nil
}

func queryToTable(db *sql.DB, query string, maxResultRows int) (*QueryResultTable, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sqlExecutionTimeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}

	result := &QueryResultTable{
		Columns: columns,
		Rows:    make([][]any, 0),
	}

	rowCount := 0
	for rows.Next() {
		rowCount++
		if rowCount > maxResultRows {
			return nil, 0, fmt.Errorf("Результат превышает ограничение в %d строк.", maxResultRows)
		}

		raw := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, 0, err
		}

		normalised := make([]any, len(columns))
		for i, cell := range raw {
			switch typed := cell.(type) {
			case nil:
				normalised[i] = nil
			case []byte:
				normalised[i] = string(typed)
			default:
				normalised[i] = typed
			}
		}
		result.Rows = append(result.Rows, normalised)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return result, rowCount, nil
}

func runLastResult(db *sql.DB, sqlText string) (*QueryResultTable, error) {
	result, _, err := executeUserStatements(db, sqlText, 200)
	return result, err
}

func validateAgainstDatasets(sqlText string, task TaskDefinition, datasets []TemplateDataset) ValidationOutcome {
	blockedKeyword := findForbiddenKeyword(sqlText, append(globalForbiddenSQLKeywords, task.ValidationConfig.ForbiddenKeywords...))
	if blockedKeyword != "" {
		return ValidationOutcome{
			Status:          "blocked",
			Summary:         fmt.Sprintf("Конструкция %s запрещена для этой задачи.", blockedKeyword),
			Details:         []DatasetValidationOutcome{},
			ExecutionTimeMs: 0,
		}
	}

	switch task.ValidationMode {
	case ValidationDDLObject:
		return validateDDLObject(sqlText, task, datasets)
	case ValidationExplainPlan:
		return validateExplainPlan(sqlText, task, datasets)
	default:
		return validateResultMatch(sqlText, task, datasets)
	}
}

func validateResultMatch(sqlText string, task TaskDefinition, datasets []TemplateDataset) ValidationOutcome {
	details := make([]DatasetValidationOutcome, 0, len(datasets))
	totalExecutionMs := 0

	for _, dataset := range datasets {
		actual := executeSQL(dataset.InitSQL, sqlText, task.ValidationConfig.MaxResultRows)
		if !actual.OK {
			status := "runtime-error"
			if strings.Contains(strings.ToLower(actual.ErrorMessage), "запрещ") {
				status = "blocked"
			}
			return ValidationOutcome{
				Status:  status,
				Summary: fmt.Sprintf("Ошибка выполнения на %s: %s", dataset.Label, actual.ErrorMessage),
				Details: []DatasetValidationOutcome{{
					DatasetID:       dataset.ID,
					Passed:          false,
					Message:         actual.ErrorMessage,
					ExecutionTimeMs: actual.ExecutionTimeMs,
				}},
				ExecutionTimeMs: totalExecutionMs + actual.ExecutionTimeMs,
			}
		}

		if actual.ExecutionTimeMs > task.ValidationConfig.MaxExecutionMs {
			return ValidationOutcome{
				Status:  "incorrect",
				Summary: fmt.Sprintf("Превышено ограничение %d мс на %s.", task.ValidationConfig.MaxExecutionMs, dataset.Label),
				Details: []DatasetValidationOutcome{{
					DatasetID:       dataset.ID,
					Passed:          false,
					Message:         fmt.Sprintf("Запрос выполнялся %d мс, что дольше лимита.", actual.ExecutionTimeMs),
					ExecutionTimeMs: actual.ExecutionTimeMs,
					Actual:          actual.Result,
				}},
				ExecutionTimeMs: totalExecutionMs + actual.ExecutionTimeMs,
			}
		}

		expected := executeSQL(dataset.InitSQL, task.ExpectedQuery, task.ValidationConfig.MaxResultRows)
		if !expected.OK || actual.Result == nil || expected.Result == nil {
			return ValidationOutcome{
				Status:          "runtime-error",
				Summary:         "Системная ошибка при построении эталонного результата.",
				Details:         details,
				ExecutionTimeMs: totalExecutionMs,
			}
		}

		mismatch := compareResults(*actual.Result, *expected.Result, task.ValidationConfig)
		totalExecutionMs += actual.ExecutionTimeMs
		details = append(details, DatasetValidationOutcome{
			DatasetID:       dataset.ID,
			Passed:          mismatch == "",
			Message:         firstNonEmpty(mismatch, fmt.Sprintf("%s: результат совпал с эталоном.", dataset.Label)),
			Actual:          actual.Result,
			Expected:        expected.Result,
			ExecutionTimeMs: actual.ExecutionTimeMs,
		})

		if mismatch != "" {
			return ValidationOutcome{
				Status:          "incorrect",
				Summary:         fmt.Sprintf("Не пройден скрытый тест %s.", dataset.Label),
				Details:         details,
				ExecutionTimeMs: totalExecutionMs,
			}
		}
	}

	return ValidationOutcome{
		Status:          "correct",
		Summary:         fmt.Sprintf("%d / %d датасетов пройдено.", len(details), len(details)),
		Details:         details,
		ExecutionTimeMs: totalExecutionMs,
	}
}

func validateDDLObject(sqlText string, task TaskDefinition, datasets []TemplateDataset) ValidationOutcome {
	if task.ValidationSpec == nil || task.ValidationSpec.DDL == nil {
		return ValidationOutcome{Status: "runtime-error", Summary: "Для DDL-задачи не задана спецификация проверки."}
	}

	spec := task.ValidationSpec.DDL
	details := make([]DatasetValidationOutcome, 0, len(datasets))
	totalExecutionMs := 0

	for _, dataset := range datasets {
		db, err := newSQLiteDB()
		if err != nil {
			return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
		}

		if err := executeStatements(db, dataset.InitSQL); err != nil {
			_ = db.Close()
			return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
		}

		startedAt := time.Now()
		if err := executeStatements(db, sqlText); err != nil {
			_ = db.Close()
			return ValidationOutcome{
				Status:  "runtime-error",
				Summary: err.Error(),
				Details: []DatasetValidationOutcome{{
					DatasetID: dataset.ID,
					Passed:    false,
					Message:   err.Error(),
				}},
			}
		}
		executionTimeMs := int(time.Since(startedAt).Milliseconds())
		totalExecutionMs += executionTimeMs

		objectResult, _, err := queryToTable(
			db,
			fmt.Sprintf("SELECT type, name, sql FROM sqlite_master WHERE type = '%s' AND name = '%s';", spec.ObjectType, spec.ObjectName),
			20,
		)
		if err != nil {
			_ = db.Close()
			return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
		}
		if objectResult == nil || len(objectResult.Rows) == 0 {
			_ = db.Close()
			details = append(details, DatasetValidationOutcome{
				DatasetID:       dataset.ID,
				Passed:          false,
				Message:         fmt.Sprintf("%s: объект %s %s не создан.", dataset.Label, spec.ObjectType, spec.ObjectName),
				ExecutionTimeMs: executionTimeMs,
			})
			return ValidationOutcome{
				Status:          "incorrect",
				Summary:         fmt.Sprintf("На %s ожидаемый объект не найден.", dataset.Label),
				Details:         details,
				ExecutionTimeMs: totalExecutionMs,
			}
		}

		objectSQL := fmt.Sprint(objectResult.Rows[0][2])
		for _, token := range spec.DefinitionMustInclude {
			if !regexp.MustCompile(`(?i)` + regexp.QuoteMeta(token)).MatchString(objectSQL) {
				_ = db.Close()
				details = append(details, DatasetValidationOutcome{
					DatasetID:       dataset.ID,
					Passed:          false,
					Message:         fmt.Sprintf("%s: в определении объекта отсутствует обязательный фрагмент %s.", dataset.Label, token),
					ExecutionTimeMs: executionTimeMs,
				})
				return ValidationOutcome{
					Status:          "incorrect",
					Summary:         fmt.Sprintf("Определение объекта на %s не прошло структурную проверку.", dataset.Label),
					Details:         details,
					ExecutionTimeMs: totalExecutionMs,
				}
			}
		}

		for _, statement := range spec.SimulationSQL {
			if err := executeStatements(db, statement); err != nil {
				_ = db.Close()
				return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
			}
		}

		if spec.VerificationQuery != "" && spec.VerificationExpected != "" {
			actual, err := runLastResult(db, spec.VerificationQuery)
			if err != nil {
				_ = db.Close()
				return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
			}
			expected, err := runLastResult(db, spec.VerificationExpected)
			if err != nil {
				_ = db.Close()
				return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
			}
			if actual == nil || expected == nil {
				_ = db.Close()
				return ValidationOutcome{Status: "runtime-error", Summary: "Системная ошибка в DDL-проверке."}
			}

			mismatch := compareResults(*actual, *expected, task.ValidationConfig)
			if mismatch != "" {
				_ = db.Close()
				details = append(details, DatasetValidationOutcome{
					DatasetID:       dataset.ID,
					Passed:          false,
					Message:         fmt.Sprintf("%s: %s", dataset.Label, mismatch),
					Actual:          actual,
					Expected:        expected,
					ExecutionTimeMs: executionTimeMs,
				})
				return ValidationOutcome{
					Status:          "incorrect",
					Summary:         fmt.Sprintf("Контрольный сценарий для %s не пройден.", spec.ObjectType),
					Details:         details,
					ExecutionTimeMs: totalExecutionMs,
				}
			}

			details = append(details, DatasetValidationOutcome{
				DatasetID:       dataset.ID,
				Passed:          true,
				Message:         fmt.Sprintf("%s: объект создан и контрольный сценарий пройден.", dataset.Label),
				Actual:          actual,
				Expected:        expected,
				ExecutionTimeMs: executionTimeMs,
			})
		} else {
			details = append(details, DatasetValidationOutcome{
				DatasetID:       dataset.ID,
				Passed:          true,
				Message:         fmt.Sprintf("%s: объект %s %s создан.", dataset.Label, spec.ObjectType, spec.ObjectName),
				ExecutionTimeMs: executionTimeMs,
			})
		}

		_ = db.Close()
	}

	return ValidationOutcome{
		Status:          "correct",
		Summary:         fmt.Sprintf("%d / %d датасетов пройдено для DDL-проверки.", len(details), len(details)),
		Details:         details,
		ExecutionTimeMs: totalExecutionMs,
	}
}

func validateExplainPlan(sqlText string, task TaskDefinition, datasets []TemplateDataset) ValidationOutcome {
	if task.ValidationSpec == nil || task.ValidationSpec.Explain == nil {
		return ValidationOutcome{Status: "runtime-error", Summary: "Для EXPLAIN-задачи не задана спецификация проверки."}
	}

	explainSpec := task.ValidationSpec.Explain
	var ddlSpec *DDLValidationSpec
	if task.ValidationSpec != nil {
		ddlSpec = task.ValidationSpec.DDL
	}

	details := make([]DatasetValidationOutcome, 0, len(datasets))
	totalExecutionMs := 0

	for _, dataset := range datasets {
		db, err := newSQLiteDB()
		if err != nil {
			return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
		}

		if err := executeStatements(db, dataset.InitSQL); err != nil {
			_ = db.Close()
			return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
		}

		startedAt := time.Now()
		if err := executeStatements(db, sqlText); err != nil {
			_ = db.Close()
			return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
		}
		executionTimeMs := int(time.Since(startedAt).Milliseconds())
		totalExecutionMs += executionTimeMs

		if ddlSpec != nil {
			indexResult, _, err := queryToTable(
				db,
				fmt.Sprintf("SELECT type, name FROM sqlite_master WHERE type = '%s' AND name = '%s';", ddlSpec.ObjectType, ddlSpec.ObjectName),
				20,
			)
			if err != nil {
				_ = db.Close()
				return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
			}
			if indexResult == nil || len(indexResult.Rows) == 0 {
				_ = db.Close()
				details = append(details, DatasetValidationOutcome{
					DatasetID:       dataset.ID,
					Passed:          false,
					Message:         fmt.Sprintf("%s: индекс %s не найден.", dataset.Label, ddlSpec.ObjectName),
					ExecutionTimeMs: executionTimeMs,
				})
				return ValidationOutcome{
					Status:          "incorrect",
					Summary:         "Ожидаемый индекс не создан.",
					Details:         details,
					ExecutionTimeMs: totalExecutionMs,
				}
			}
		}

		planTable, err := runLastResult(db, "EXPLAIN QUERY PLAN "+explainSpec.TargetSQL)
		if err != nil {
			_ = db.Close()
			return ValidationOutcome{Status: "runtime-error", Summary: err.Error()}
		}
		if planTable == nil {
			_ = db.Close()
			return ValidationOutcome{Status: "runtime-error", Summary: "Не удалось построить план выполнения."}
		}

		planText := flattenRow(flattenTableRows(planTable.Rows))
		missingKeyword := ""
		for _, keyword := range explainSpec.RequiredPlanKeywords {
			if !regexp.MustCompile(`(?i)` + regexp.QuoteMeta(keyword)).MatchString(planText) {
				missingKeyword = keyword
				break
			}
		}

		if missingKeyword != "" {
			_ = db.Close()
			details = append(details, DatasetValidationOutcome{
				DatasetID:       dataset.ID,
				Passed:          false,
				Message:         fmt.Sprintf("%s: план не содержит обязательный фрагмент %s.", dataset.Label, missingKeyword),
				Actual:          planTable,
				ExecutionTimeMs: executionTimeMs,
			})
			return ValidationOutcome{
				Status:          "incorrect",
				Summary:         "План выполнения не соответствует ожиданиям.",
				Details:         details,
				ExecutionTimeMs: totalExecutionMs,
			}
		}

		details = append(details, DatasetValidationOutcome{
			DatasetID:       dataset.ID,
			Passed:          true,
			Message:         fmt.Sprintf("%s: план выполнения содержит ожидаемые признаки.", dataset.Label),
			Actual:          planTable,
			ExecutionTimeMs: executionTimeMs,
		})
		_ = db.Close()
	}

	return ValidationOutcome{
		Status:          "correct",
		Summary:         fmt.Sprintf("%d / %d датасетов пройдено для EXPLAIN-проверки.", len(details), len(details)),
		Details:         details,
		ExecutionTimeMs: totalExecutionMs,
	}
}

func flattenTableRows(rows [][]any) []any {
	result := make([]any, 0)
	for _, row := range rows {
		result = append(result, row...)
	}
	return result
}

func firstNonEmpty(primary string, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}
