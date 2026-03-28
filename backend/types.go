package main

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
	ValidationDDLObject   ValidationMode = "ddl-object"
	ValidationExplainPlan ValidationMode = "explain-plan"
)

type User struct {
	ID           string `json:"id"`
	FullName     string `json:"fullName"`
	Login        string `json:"login"`
	PasswordHash string `json:"passwordHash"`
	Role         Role   `json:"role"`
	GroupID      string `json:"groupId,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

type Group struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Stream string `json:"stream"`
}

type TableReference struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

type TableColumn struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	IsPrimaryKey bool            `json:"isPrimaryKey,omitempty"`
	References   *TableReference `json:"references,omitempty"`
	Description  string          `json:"description,omitempty"`
}

type TablePosition struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type TableDefinition struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Position    TablePosition `json:"position"`
	Columns     []TableColumn `json:"columns"`
	SampleRows  [][]string    `json:"sampleRows"`
}

type TemplateDataset struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	SchemaSQL   string `json:"schemaSql,omitempty"`
	SeedSQL     string `json:"seedSql,omitempty"`
	InitSQL     string `json:"initSql"`
}

type DDLValidationSpec struct {
	ObjectType            string   `json:"objectType"`
	ObjectName            string   `json:"objectName"`
	ObjectTargetTable     string   `json:"objectTargetTable,omitempty"`
	DefinitionMustInclude []string `json:"definitionMustInclude,omitempty"`
	SimulationSQL         []string `json:"simulationSql,omitempty"`
	VerificationQuery     string   `json:"verificationQuery,omitempty"`
	VerificationExpected  string   `json:"verificationExpectedQuery,omitempty"`
}

type ExplainValidationSpec struct {
	TargetSQL            string   `json:"targetSql"`
	RequiredPlanKeywords []string `json:"requiredPlanKeywords"`
}

type ValidationSpec struct {
	DDL     *DDLValidationSpec     `json:"ddl,omitempty"`
	Explain *ExplainValidationSpec `json:"explain,omitempty"`
}

type DBTemplate struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Level       string            `json:"level"`
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
	Difficulty       string           `json:"difficulty"`
	TaskType         string           `json:"taskType"`
	Constructs       []string         `json:"constructs"`
	ValidationMode   ValidationMode   `json:"validationMode"`
	TemplateID       string           `json:"templateId"`
	DatasetIDs       []string         `json:"datasetIds"`
	StarterSQL       string           `json:"starterSql"`
	ExpectedQuery    string           `json:"expectedQuery"`
	ValidationConfig ValidationConfig `json:"validationConfig"`
	ValidationSpec   *ValidationSpec  `json:"validationSpec,omitempty"`
	Hints            []string         `json:"hints"`
}

type SeminarSettings struct {
	LeaderboardEnabled       bool `json:"leaderboardEnabled"`
	AutoValidationEnabled    bool `json:"autoValidationEnabled"`
	NotificationsEnabled     bool `json:"notificationsEnabled"`
	DiagnosticsVisible       bool `json:"diagnosticsVisible"`
	ReferenceSolutionVisible bool `json:"referenceSolutionVisible"`
	SubmissionsFrozen        bool `json:"submissionsFrozen"`
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
	StartTime   string          `json:"startTime"`
	EndTime     string          `json:"endTime"`
	Status      SeminarStatus   `json:"status"`
	Settings    SeminarSettings `json:"settings"`
}

type PlaygroundChallenge struct {
	ID             string          `json:"id"`
	TemplateID     string          `json:"templateId"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Difficulty     string          `json:"difficulty"`
	Topic          string          `json:"topic"`
	Constructs     []string        `json:"constructs"`
	DatasetIDs     []string        `json:"datasetIds"`
	StarterSQL     string          `json:"starterSql"`
	ExpectedQuery  string          `json:"expectedQuery"`
	FeedbackMode   string          `json:"feedbackMode"`
	ValidationMode ValidationMode  `json:"validationMode,omitempty"`
	ValidationSpec *ValidationSpec `json:"validationSpec,omitempty"`
}

type QueryResultTable struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

type QueryRun struct {
	ID                    string            `json:"id"`
	UserID                string            `json:"userId"`
	Role                  Role              `json:"role"`
	Context               string            `json:"context"`
	SeminarID             string            `json:"seminarId,omitempty"`
	PlaygroundChallengeID string            `json:"playgroundChallengeId,omitempty"`
	TaskID                string            `json:"taskId,omitempty"`
	DatasetID             string            `json:"datasetId"`
	SQLText               string            `json:"sqlText"`
	Status                string            `json:"status"`
	ExecutionTimeMs       int               `json:"executionTimeMs"`
	RowCount              int               `json:"rowCount"`
	Result                *QueryResultTable `json:"result,omitempty"`
	ErrorMessage          string            `json:"errorMessage,omitempty"`
	CreatedAt             string            `json:"createdAt"`
}

type DatasetValidationOutcome struct {
	DatasetID       string            `json:"datasetId"`
	Passed          bool              `json:"passed"`
	Message         string            `json:"message"`
	Actual          *QueryResultTable `json:"actual,omitempty"`
	Expected        *QueryResultTable `json:"expected,omitempty"`
	ExecutionTimeMs int               `json:"executionTimeMs"`
}

type SubmissionValidationDetails struct {
	Datasets []DatasetValidationOutcome `json:"datasets"`
	Summary  string                     `json:"summary"`
}

type Submission struct {
	ID                string                      `json:"id"`
	UserID            string                      `json:"userId"`
	SeminarID         string                      `json:"seminarId"`
	TaskID            string                      `json:"taskId"`
	SQLText           string                      `json:"sqlText"`
	SubmittedAt       string                      `json:"submittedAt"`
	Status            string                      `json:"status"`
	ExecutionTimeMs   int                         `json:"executionTimeMs"`
	ValidationDetails SubmissionValidationDetails `json:"validationDetails"`
}

type Notification struct {
	ID        string `json:"id"`
	SeminarID string `json:"seminarId"`
	CreatedAt string `json:"createdAt"`
	Level     string `json:"level"`
	Title     string `json:"title"`
	Body      string `json:"body"`
}

type EventLog struct {
	ID                    string         `json:"id"`
	UserID                string         `json:"userId"`
	Role                  Role           `json:"role"`
	SessionID             string         `json:"sessionId"`
	SeminarID             string         `json:"seminarId,omitempty"`
	PlaygroundChallengeID string         `json:"playgroundChallengeId,omitempty"`
	TaskID                string         `json:"taskId,omitempty"`
	EventType             string         `json:"eventType"`
	SQLText               string         `json:"sqlText,omitempty"`
	Status                string         `json:"status,omitempty"`
	ExecutionTimeMs       int            `json:"executionTimeMs,omitempty"`
	Payload               map[string]any `json:"payload"`
	CreatedAt             string         `json:"createdAt"`
}

type SeminarRuntime struct {
	Status   SeminarStatus   `json:"status"`
	Settings SeminarSettings `json:"settings"`
}

type SeminarMetaOverride struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	AccessCode  string `json:"accessCode"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
}

type PlatformRuntime struct {
	IsAuthenticated               bool                           `json:"isAuthenticated"`
	CurrentUserID                 string                         `json:"currentUserId"`
	SessionID                     string                         `json:"sessionId"`
	CreatedUsers                  []User                         `json:"createdUsers"`
	CreatedGroups                 []Group                        `json:"createdGroups"`
	UserOverrides                 map[string]User                `json:"userOverrides"`
	GroupOverrides                map[string]Group               `json:"groupOverrides"`
	DeletedUserIDs                []string                       `json:"deletedUserIds"`
	DeletedGroupIDs               []string                       `json:"deletedGroupIds"`
	SeminarMeta                   map[string]SeminarMetaOverride `json:"seminarMeta"`
	SeminarGroupIDs               map[string]string              `json:"seminarGroupIds"`
	SeminarStudentIDs             map[string][]string            `json:"seminarStudentIds"`
	SeminarTaskIDs                map[string][]string            `json:"seminarTaskIds"`
	GroupRosterOverrides          map[string]bool                `json:"groupRosterOverrides"`
	SelectedSeminarByUser         map[string]string              `json:"selectedSeminarByUser"`
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
	LastPickedStudentID           string                         `json:"lastPickedStudentId,omitempty"`
}

type SeedData struct {
	Users                []User                `json:"users"`
	Groups               []Group               `json:"groups"`
	Templates            []DBTemplate          `json:"templates"`
	Seminars             []Seminar             `json:"seminars"`
	Tasks                []TaskDefinition      `json:"tasks"`
	PlaygroundChallenges []PlaygroundChallenge `json:"playgroundChallenges"`
	Runtime              PlatformRuntime       `json:"runtime"`
}

type CatalogData struct {
	Users                []User                `json:"users"`
	Groups               []Group               `json:"groups"`
	Templates            []DBTemplate          `json:"templates"`
	Seminars             []Seminar             `json:"seminars"`
	Tasks                []TaskDefinition      `json:"tasks"`
	PlaygroundChallenges []PlaygroundChallenge `json:"playgroundChallenges"`
}

type PlaygroundSelection struct {
	SelectedPlaygroundTemplateID  string `json:"selectedPlaygroundTemplateId"`
	SelectedPlaygroundChallengeID string `json:"selectedPlaygroundChallengeId"`
	SelectedPlaygroundDatasetID   string `json:"selectedPlaygroundDatasetId"`
}

type ServerState struct {
	Runtime                  PlatformRuntime                `json:"runtime"`
	UserPlaygroundSelections map[string]PlaygroundSelection `json:"userPlaygroundSelections"`
}

type ValidationOutcome struct {
	Status          string                     `json:"status"`
	Summary         string                     `json:"summary"`
	Details         []DatasetValidationOutcome `json:"details"`
	ExecutionTimeMs int                        `json:"executionTimeMs"`
}

type LoginResponse struct {
	Token   string          `json:"token"`
	Catalog CatalogData     `json:"catalog"`
	Runtime PlatformRuntime `json:"runtime"`
}

type RuntimeResponse struct {
	Catalog CatalogData     `json:"catalog"`
	Runtime PlatformRuntime `json:"runtime"`
}

type WSMessage struct {
	Type    string          `json:"type"`
	Catalog CatalogData     `json:"catalog"`
	Runtime PlatformRuntime `json:"runtime"`
}
