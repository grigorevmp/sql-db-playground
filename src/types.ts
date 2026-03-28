export type Role = 'student' | 'teacher' | 'admin'

export type SeminarStatus = 'scheduled' | 'live' | 'closed'

export type ValidationMode = 'result-match' | 'ddl-object' | 'explain-plan'

export type Difficulty = 'easy' | 'medium' | 'hard'

export type FeedbackMode = 'full' | 'preview' | 'row-count' | 'match-only'

export type QueryContext = 'seminar' | 'playground'

export type SubmissionStatus = 'correct' | 'incorrect' | 'runtime-error' | 'blocked'

export interface User {
  id: string
  fullName: string
  login: string
  passwordHash: string
  role: Role
  groupId?: string
  createdAt: string
}

export interface Group {
  id: string
  title: string
  stream: string
}

export interface TableColumn {
  name: string
  type: string
  isPrimaryKey?: boolean
  references?: {
    table: string
    column: string
  }
  description?: string
}

export interface TableDefinition {
  name: string
  description: string
  position: {
    x: number
    y: number
  }
  columns: TableColumn[]
  sampleRows: string[][]
}

export interface TemplateDataset {
  id: string
  label: string
  description: string
  schemaSql?: string
  seedSql?: string
  initSql: string
}

export interface DdlValidationSpec {
  objectType: 'view' | 'trigger' | 'index'
  objectName: string
  objectTargetTable?: string
  definitionMustInclude?: string[]
  simulationSql?: string[]
  verificationQuery?: string
  verificationExpectedQuery?: string
}

export interface ExplainValidationSpec {
  targetSql: string
  requiredPlanKeywords: string[]
}

export interface ValidationSpec {
  ddl?: DdlValidationSpec
  explain?: ExplainValidationSpec
}

export interface DBTemplate {
  id: string
  title: string
  description: string
  level: Difficulty
  topics: string[]
  tables: TableDefinition[]
  datasets: TemplateDataset[]
}

export interface ValidationConfig {
  orderMatters: boolean
  columnNamesMatter: boolean
  numericTolerance: number
  maxExecutionMs: number
  maxResultRows: number
  forbiddenKeywords: string[]
}

export interface TaskDefinition {
  id: string
  seminarId: string
  title: string
  description: string
  difficulty: Difficulty
  taskType: string
  constructs: string[]
  validationMode: ValidationMode
  templateId: string
  datasetIds: string[]
  starterSql: string
  expectedQuery: string
  validationConfig: ValidationConfig
  validationSpec?: ValidationSpec
  hints: string[]
}

export interface SeminarSettings {
  leaderboardEnabled: boolean
  autoValidationEnabled: boolean
  notificationsEnabled: boolean
  diagnosticsVisible: boolean
  referenceSolutionVisible: boolean
  submissionsFrozen: boolean
}

export interface Seminar {
  id: string
  title: string
  description: string
  groupId: string
  teacherId: string
  templateId: string
  taskIds: string[]
  studentIds: string[]
  accessCode: string
  startTime: string
  endTime: string
  status: SeminarStatus
  settings: SeminarSettings
}

export interface PlaygroundChallenge {
  id: string
  templateId: string
  title: string
  description: string
  difficulty: Difficulty
  topic: string
  constructs: string[]
  datasetIds: string[]
  starterSql: string
  expectedQuery: string
  feedbackMode: FeedbackMode
  validationMode?: ValidationMode
  validationSpec?: ValidationSpec
}

export interface QueryResultTable {
  columns: string[]
  rows: Array<Array<string | number | null>>
}

export interface QueryRun {
  id: string
  userId: string
  role: Role
  context: QueryContext
  seminarId?: string
  playgroundChallengeId?: string
  taskId?: string
  datasetId: string
  sqlText: string
  status: 'success' | 'error' | 'blocked'
  executionTimeMs: number
  rowCount: number
  result?: QueryResultTable
  errorMessage?: string
  createdAt: string
}

export interface DatasetValidationOutcome {
  datasetId: string
  passed: boolean
  message: string
  actual?: QueryResultTable
  expected?: QueryResultTable
  executionTimeMs: number
}

export interface Submission {
  id: string
  userId: string
  seminarId: string
  taskId: string
  sqlText: string
  submittedAt: string
  status: SubmissionStatus
  executionTimeMs: number
  validationDetails: {
    datasets: DatasetValidationOutcome[]
    summary: string
  }
}

export interface Notification {
  id: string
  seminarId: string
  createdAt: string
  level: 'info' | 'success' | 'warning'
  title: string
  body: string
}

export interface EventLog {
  id: string
  userId: string
  role: Role
  sessionId: string
  seminarId?: string
  playgroundChallengeId?: string
  taskId?: string
  eventType: string
  sqlText?: string
  status?: string
  executionTimeMs?: number
  payload: Record<string, string | number | boolean | null>
  createdAt: string
}

export interface SeminarRuntime {
  status: SeminarStatus
  settings: SeminarSettings
}

export interface SeminarMetaOverride {
  title: string
  description: string
  accessCode: string
  startTime: string
  endTime: string
}

export interface PlatformRuntime {
  isAuthenticated: boolean
  currentUserId: string
  sessionId: string
  createdUsers: User[]
  createdGroups: Group[]
  userOverrides: Record<string, User>
  groupOverrides: Record<string, Group>
  deletedUserIds: string[]
  deletedGroupIds: string[]
  seminarMeta: Record<string, SeminarMetaOverride>
  seminarStudentIds: Record<string, string[]>
  seminarTaskIds: Record<string, string[]>
  selectedSeminarByUser: Record<string, string>
  selectedTaskByUser: Record<string, string>
  drafts: Record<string, string>
  selectedPlaygroundTemplateId: string
  selectedPlaygroundChallengeId: string
  selectedPlaygroundDatasetId: string
  seminarRuntime: Record<string, SeminarRuntime>
  importedTemplates: DBTemplate[]
  createdTasks: TaskDefinition[]
  createdSeminars: Seminar[]
  queryRuns: QueryRun[]
  submissions: Submission[]
  notifications: Notification[]
  eventLogs: EventLog[]
  lastPickedStudentId?: string
}

export interface SeedData {
  users: User[]
  groups: Group[]
  templates: DBTemplate[]
  seminars: Seminar[]
  tasks: TaskDefinition[]
  playgroundChallenges: PlaygroundChallenge[]
  runtime: PlatformRuntime
}

export interface AppCatalog {
  users: User[]
  groups: Group[]
  templates: DBTemplate[]
  seminars: Seminar[]
  tasks: TaskDefinition[]
  playgroundChallenges: PlaygroundChallenge[]
}
