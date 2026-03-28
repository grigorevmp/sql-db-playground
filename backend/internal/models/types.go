package models

import "time"

type Role string

const (
	RoleStudent Role = "student"
	RoleTeacher Role = "teacher"
	RoleAdmin   Role = "admin"
)

type SeminarStatus string

const (
	SeminarScheduled SeminarStatus = "scheduled"
	SeminarLive      SeminarStatus = "live"
	SeminarClosed    SeminarStatus = "closed"
)

type ValidationMode string

const (
	ValidationResultMatch ValidationMode = "result-match"
	ValidationDdlObject   ValidationMode = "ddl-object"
	ValidationExplainPlan ValidationMode = "explain-plan"
)

type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

type FeedbackMode string

const (
	FeedbackFull      FeedbackMode = "full"
	FeedbackPreview   FeedbackMode = "preview"
	FeedbackRowCount  FeedbackMode = "row-count"
	FeedbackMatchOnly FeedbackMode = "match-only"
)

type QueryContext string

const (
	QueryContextSeminar    QueryContext = "seminar"
	QueryContextPlayground QueryContext = "playground"
)

type SubmissionStatus string

const (
	SubmissionCorrect      SubmissionStatus = "correct"
	SubmissionIncorrect    SubmissionStatus = "incorrect"
	SubmissionRuntimeError SubmissionStatus = "runtime-error"
	SubmissionBlocked      SubmissionStatus = "blocked"
)

type User struct {
	ID           string    `json:"id"`
	FullName     string    `json:"fullName"`
	Login        string    `json:"login"`
	PasswordHash string    `json:"passwordHash,omitempty"`
	Role         Role      `json:"role"`
	GroupID      *string   `json:"groupId,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Group struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Stream string `json:"stream"`
}

type TableColumn struct {
	Name         string           `json:"name"`
	Type         string           `json:"type"`
	IsPrimaryKey bool             `json:"isPrimaryKey,omitempty"`
	References   *ColumnReference `json:"references,omitempty"`
	Description  string           `json:"description,omitempty"`
}

type ColumnReference struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

type TableDefinition struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Position    Position      `json:"position"`
	Columns     []TableColumn `json:"columns"`
	SampleRows  [][]string    `json:"sampleRows"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type TemplateDataset struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	SchemaSql   string `json:"schemaSql,omitempty"`
	SeedSql     string `json:"seedSql,omitempty"`
	InitSql     string `json:"initSql"`
}

type DdlValidationSpec struct {
	ObjectType                string   `json:"objectType"`
	ObjectName                string   `json:"objectName"`
	ObjectTargetTable         string   `json:"objectTargetTable,omitempty"`
	DefinitionMustInclude     []string `json:"definitionMustInclude,omitempty"`
	SimulationSql             []string `json:"simulationSql,omitempty"`
	VerificationQuery         string   `json:"verificationQuery,omitempty"`
	VerificationExpectedQuery string   `json:"verificationExpectedQuery,omitempty"`
}

type ExplainValidationSpec struct {
	TargetSql            string   `json:"targetSql"`
	RequiredPlanKeywords []string `json:"requiredPlanKeywords"`
}

type ValidationSpec struct {
	DDL     *DdlValidationSpec     `json:"ddl,omitempty"`
	Explain *ExplainValidationSpec `json:"explain,omitempty"`
}

type DBTemplate struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Level       Difficulty        `json:"level"`
	Topics      []string          `json:"topics"`
	Tables      []TableDefinition `json:"tables"`
	Datasets    []TemplateDataset `json:"datasets"`
}

type ValidationConfig struct {
	OrderMatters      bool     `json:"orderMatters"`
	ColumnNamesMatter bool     `json:"columnNamesMatter"`
	NumericTolerance  float64  `json:"numericTolerance"`
	MaxExecutionMs    int      `json:"maxExecutionMs"`
	MaxResultRows     int      `json:"maxResultRows"`
	ForbiddenKeywords []string `json:"forbiddenKeywords"`
}

type TaskDefinition struct {
	ID               string           `json:"id"`
	SeminarID        string           `json:"seminarId"`
	Title            string           `json:"title"`
	Description      string           `json:"description"`
	Difficulty       Difficulty       `json:"difficulty"`
	TaskType         string           `json:"taskType"`
	Constructs       []string         `json:"constructs"`
	ValidationMode   ValidationMode   `json:"validationMode"`
	TemplateID       string           `json:"templateId"`
	DatasetIDs       []string         `json:"datasetIds"`
	StarterSql       string           `json:"starterSql"`
	ExpectedQuery    string           `json:"expectedQuery"`
	ValidationConfig ValidationConfig `json:"validationConfig"`
	ValidationSpec   *ValidationSpec  `json:"validationSpec,omitempty"`
	Hints            []string         `json:"hints"`
}

type SeminarSettings struct {
	LeaderboardEnabled    bool `json:"leaderboardEnabled"`
	AutoValidationEnabled bool `json:"autoValidationEnabled"`
	NotificationsEnabled  bool `json:"notificationsEnabled"`
	DiagnosticsVisible    bool `json:"diagnosticsVisible"`
	SubmissionsFrozen     bool `json:"submissionsFrozen"`
}

type Seminar struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	GroupID     string          `json:"groupId"`
	TeacherID   string          `json:"teacherId"`
	TemplateID  string          `json:"templateId"`
	TaskIDs     []string        `json:"taskIds"`
	StudentIDs  []string        `json:"studentIds"`
	AccessCode  string          `json:"accessCode"`
	StartTime   time.Time       `json:"startTime"`
	EndTime     time.Time       `json:"endTime"`
	Status      SeminarStatus   `json:"status"`
	Settings    SeminarSettings `json:"settings"`
}

type PlaygroundChallenge struct {
	ID             string          `json:"id"`
	TemplateID     string          `json:"templateId"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Difficulty     Difficulty      `json:"difficulty"`
	Topic          string          `json:"topic"`
	Constructs     []string        `json:"constructs"`
	DatasetIDs     []string        `json:"datasetIds"`
	StarterSql     string          `json:"starterSql"`
	ExpectedQuery  string          `json:"expectedQuery"`
	FeedbackMode   FeedbackMode    `json:"feedbackMode"`
	ValidationMode *ValidationMode `json:"validationMode,omitempty"`
	ValidationSpec *ValidationSpec `json:"validationSpec,omitempty"`
}

type QueryResultTable struct {
	Columns []string        `json:"columns"`
	Rows    [][]interface{} `json:"rows"`
}

type QueryRun struct {
	ID                    string            `json:"id"`
	UserID                string            `json:"userId"`
	Role                  Role              `json:"role"`
	Context               QueryContext      `json:"context"`
	SeminarID             *string           `json:"seminarId,omitempty"`
	PlaygroundChallengeID *string           `json:"playgroundChallengeId,omitempty"`
	TaskID                *string           `json:"taskId,omitempty"`
	DatasetID             string            `json:"datasetId"`
	SqlText               string            `json:"sqlText"`
	Status                string            `json:"status"`
	ExecutionTimeMs       int               `json:"executionTimeMs"`
	RowCount              int               `json:"rowCount"`
	Result                *QueryResultTable `json:"result,omitempty"`
	ErrorMessage          *string           `json:"errorMessage,omitempty"`
	CreatedAt             time.Time         `json:"createdAt"`
}

type DatasetValidationOutcome struct {
	DatasetID       string            `json:"datasetId"`
	Passed          bool              `json:"passed"`
	Message         string            `json:"message"`
	Actual          *QueryResultTable `json:"actual,omitempty"`
	Expected        *QueryResultTable `json:"expected,omitempty"`
	ExecutionTimeMs int               `json:"executionTimeMs"`
}

type Submission struct {
	ID                string            `json:"id"`
	UserID            string            `json:"userId"`
	SeminarID         string            `json:"seminarId"`
	TaskID            string            `json:"taskId"`
	SqlText           string            `json:"sqlText"`
	SubmittedAt       time.Time         `json:"submittedAt"`
	Status            SubmissionStatus  `json:"status"`
	ExecutionTimeMs   int               `json:"executionTimeMs"`
	ValidationDetails ValidationDetails `json:"validationDetails"`
}

type ValidationDetails struct {
	Datasets []DatasetValidationOutcome `json:"datasets"`
	Summary  string                     `json:"summary"`
}

type Notification struct {
	ID        string    `json:"id"`
	SeminarID string    `json:"seminarId"`
	CreatedAt time.Time `json:"createdAt"`
	Level     string    `json:"level"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
}

type EventLog struct {
	ID                    string                 `json:"id"`
	UserID                string                 `json:"userId"`
	Role                  Role                   `json:"role"`
	SessionID             string                 `json:"sessionId"`
	SeminarID             *string                `json:"seminarId,omitempty"`
	PlaygroundChallengeID *string                `json:"playgroundChallengeId,omitempty"`
	TaskID                *string                `json:"taskId,omitempty"`
	EventType             string                 `json:"eventType"`
	SqlText               *string                `json:"sqlText,omitempty"`
	Status                *string                `json:"status,omitempty"`
	ExecutionTimeMs       *int                   `json:"executionTimeMs,omitempty"`
	Payload               map[string]interface{} `json:"payload"`
	CreatedAt             time.Time              `json:"createdAt"`
}

type SeminarRuntime struct {
	Status   SeminarStatus   `json:"status"`
	Settings SeminarSettings `json:"settings"`
}

type SeminarMetaOverride struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	AccessCode  string    `json:"accessCode"`
	StartTime   time.Time `json:"startTime"`
	EndTime     time.Time `json:"endTime"`
}

type UserPlaygroundSelection struct {
	SelectedPlaygroundTemplateID  string `json:"selectedPlaygroundTemplateId"`
	SelectedPlaygroundChallengeID string `json:"selectedPlaygroundChallengeId"`
	SelectedPlaygroundDatasetID   string `json:"selectedPlaygroundDatasetId"`
}

type PlatformRuntime struct {
	IsAuthenticated               bool                           `json:"isAuthenticated"`
	CurrentUserID                 string                         `json:"currentUserId"`
	SessionID                     string                         `json:"sessionId"`
	SeminarMeta                   map[string]SeminarMetaOverride `json:"seminarMeta"`
	SeminarTaskIDs                map[string][]string            `json:"seminarTaskIds"`
	SelectedTaskByUser            map[string]string              `json:"selectedTaskByUser"`
	Drafts                        map[string]string              `json:"drafts"`
	SelectedPlaygroundTemplateID  string                         `json:"selectedPlaygroundTemplateId"`
	SelectedPlaygroundChallengeID string                         `json:"selectedPlaygroundChallengeId"`
	SelectedPlaygroundDatasetID   string                         `json:"selectedPlaygroundDatasetId"`
	SeminarRuntime                map[string]SeminarRuntime      `json:"seminarRuntime"`
	ImportedTemplates             []DBTemplate                   `json:"importedTemplates"`
	CreatedTasks                  []TaskDefinition               `json:"createdTasks"`
	CreatedSeminars               []Seminar                      `json:"createdSeminars"`
	QueryRuns                     []QueryRun                     `json:"queryRuns"`
	Submissions                   []Submission                   `json:"submissions"`
	Notifications                 []Notification                 `json:"notifications"`
	EventLogs                     []EventLog                     `json:"eventLogs"`
	LastPickedStudentID           *string                        `json:"lastPickedStudentId,omitempty"`
}

type PublicUser struct {
	ID        string    `json:"id"`
	FullName  string    `json:"fullName"`
	Login     string    `json:"login"`
	Role      Role      `json:"role"`
	GroupID   *string   `json:"groupId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
