import {
  HashRouter,
  NavLink,
  Navigate,
  Route,
  Routes,
  useNavigate,
} from 'react-router-dom'
import {
  startTransition,
  useDeferredValue,
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
} from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { sql } from '@codemirror/lang-sql'
import clsx from 'clsx'

import ksitLogo from './assets/ksit-logo.svg'
import { downloadCsv, downloadWorkbook } from './lib/export'
import { api, clearStoredToken, getStoredToken, setStoredToken, type RuntimeSocket } from './lib/api'

import type {
  AppCatalog,
  DBTemplate,
  Group,
  PlatformRuntime,
  PlaygroundChallenge,
  QueryContext,
  QueryResultTable,
  QueryRun,
  Role,
  Seminar,
  SeminarMetaOverride,
  Submission,
  TaskDefinition,
  User,
} from './types'

const emptyRuntime = (): PlatformRuntime => ({
  isAuthenticated: false,
  currentUserId: '',
  sessionId: '',
  createdUsers: [],
  createdGroups: [],
  userOverrides: {},
  groupOverrides: {},
  deletedUserIds: [],
  deletedGroupIds: [],
  seminarMeta: {},
  seminarStudentIds: {},
  seminarTaskIds: {},
  selectedSeminarByUser: {},
  selectedTaskByUser: {},
  drafts: {},
  selectedPlaygroundTemplateId: '',
  selectedPlaygroundChallengeId: '',
  selectedPlaygroundDatasetId: '',
  seminarRuntime: {},
  importedTemplates: [],
  createdTasks: [],
  createdSeminars: [],
  queryRuns: [],
  submissions: [],
  notifications: [],
  eventLogs: [],
})

const emptyCatalog = (): AppCatalog => ({
  users: [],
  groups: [],
  templates: [],
  seminars: [],
  tasks: [],
  playgroundChallenges: [],
})

const anonymousUser: User = {
  id: '',
  fullName: 'Гость',
  login: '',
  passwordHash: '',
  role: 'student',
  createdAt: '',
}

const emptySeminar: Seminar = {
  id: '',
  title: 'Семинар не выбран',
  description: '',
  groupId: '',
  teacherId: '',
  templateId: '',
  taskIds: [],
  studentIds: [],
  accessCode: '',
  startTime: new Date().toISOString(),
  endTime: new Date().toISOString(),
  status: 'scheduled',
  settings: {
    leaderboardEnabled: false,
    autoValidationEnabled: false,
    notificationsEnabled: false,
    diagnosticsVisible: false,
    referenceSolutionVisible: false,
    submissionsFrozen: false,
  },
}

let appUsers: User[] = []
let appGroups: Group[] = []

const formatDateTime = (value: string) =>
  new Intl.DateTimeFormat('ru-RU', {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))

const roleLabel: Record<Role, string> = {
  student: 'Студент',
  teacher: 'Преподаватель',
  admin: 'Администратор',
}

const seminarStatusLabel: Record<Seminar['status'], string> = {
  scheduled: 'Запланирован',
  live: 'Открыт',
  closed: 'Закрыт',
}

const draftKey = (userId: string, context: QueryContext, entityId: string) =>
  `${userId}:${context}:${entityId}`

const queryStatusTone = (status?: string) => {
  if (status === 'correct' || status === 'success') {
    return 'success'
  }

  if (status === 'blocked') {
    return 'warning'
  }

  if (status === 'incorrect' || status === 'runtime-error' || status === 'error') {
    return 'danger'
  }

  return 'neutral'
}

const queryStatusLabel = (status?: string) => {
  if (status === 'correct') {
    return 'Решение принято'
  }

  if (status === 'incorrect') {
    return 'Не принято'
  }

  if (status === 'runtime-error') {
    return 'Ошибка SQL'
  }

  if (status === 'blocked') {
    return 'Запрещено'
  }

  if (status === 'success') {
    return 'Запрос корректен'
  }

  if (status === 'error') {
    return 'Ошибка выполнения'
  }

  if (status === 'waiting') {
    return 'Нет данных'
  }

  if (status === 'new') {
    return 'Новая'
  }

  return 'Нет данных'
}

const localizeUiText = (text: string) =>
  text
    .replaceAll('Dataset', 'Датасет')
    .replaceAll('preview', 'предварительный просмотр')
    .replaceAll('row-count', 'подсчёт строк')
    .replaceAll('match-only', 'только проверка совпадения')
    .replaceAll('runtime-error', 'ошибка SQL')
    .replaceAll('incorrect', 'не принято')
    .replaceAll('success', 'запрос корректен')

const resolveSeminar = (seminar: Seminar, runtime: PlatformRuntime): Seminar => {
  const meta = runtime.seminarMeta[seminar.id]
  const seminarRuntime = runtime.seminarRuntime[seminar.id]

  return {
    ...seminar,
    ...(meta ?? {}),
    status: seminarRuntime?.status ?? seminar.status,
    settings: seminarRuntime?.settings ?? seminar.settings,
    taskIds: runtime.seminarTaskIds[seminar.id] ?? seminar.taskIds,
  }
}

const getUser = (userId: string) => appUsers.find((user) => user.id === userId)
const getGroup = (groupId: string) => appGroups.find((group) => group.id === groupId)

const getTaskCatalog = (tasks: TaskDefinition[]) => tasks

const getTaskById = (taskId: string, tasks: TaskDefinition[]) => tasks.find((task) => task.id === taskId)

const pickLatestByTask = (submissions: Submission[], userId: string, taskId: string, seminarId?: string) =>
  [...submissions]
    .filter((submission) =>
      submission.userId === userId
      && submission.taskId === taskId
      && (!seminarId || submission.seminarId === seminarId))
    .sort((left, right) => right.submittedAt.localeCompare(left.submittedAt))[0]

const pickLatestRun = (
  queryRuns: QueryRun[],
  userId: string,
  context: QueryContext,
  targetId: string,
  seminarId?: string,
) =>
  [...queryRuns]
    .filter((run) =>
      run.userId === userId
        && run.context === context
        && (!seminarId || run.seminarId === seminarId)
        && (context === 'seminar' ? run.taskId === targetId : run.playgroundChallengeId === targetId))
    .sort((left, right) => right.createdAt.localeCompare(left.createdAt))[0]

const computeLeaderboard = (seminar: Seminar, submissions: Submission[]) => {
  const scoped = submissions.filter((submission) => submission.seminarId === seminar.id)
  const scores = seminar.studentIds
    .map((studentId) => {
      const user = getUser(studentId)
      if (!user) {
        return null
      }

      const solvedTasks = seminar.taskIds.filter((taskId) =>
        scoped.some((submission) =>
          submission.userId === studentId
          && submission.taskId === taskId
          && submission.status === 'correct'))

      const correctAttempts = scoped.filter(
        (submission) => submission.userId === studentId && submission.status === 'correct',
      )

      const allAttempts = scoped.filter((submission) => submission.userId === studentId)
      const firstSolvedAt = correctAttempts.map((submission) => submission.submittedAt).sort()[0]
      const speedScore = correctAttempts.reduce(
        (accumulator, submission) => accumulator + submission.executionTimeMs,
        0,
      )

      return {
        user,
        solvedCount: solvedTasks.length,
        attemptsCount: allAttempts.length,
        speedScore,
        firstSolvedAt,
      }
    })
    .filter((entry): entry is NonNullable<typeof entry> => Boolean(entry))
    .sort((left, right) => {
      if (right.solvedCount !== left.solvedCount) {
        return right.solvedCount - left.solvedCount
      }

      if (left.speedScore !== right.speedScore) {
        return left.speedScore - right.speedScore
      }

      return (left.firstSolvedAt ?? '9999').localeCompare(right.firstSolvedAt ?? '9999')
    })

  return scores.map((entry, index) => ({
    rank: index + 1,
    ...entry,
  }))
}

const computeCoverageMetrics = (seminar: Seminar, runtime: PlatformRuntime) => {
  const correctSubmissions = runtime.submissions.filter(
    (submission) => submission.seminarId === seminar.id && submission.status === 'correct',
  )
  const activeStudents = seminar.studentIds.filter((studentId) =>
    runtime.eventLogs.some((event) => event.userId === studentId && event.seminarId === seminar.id))

  return {
    solvedTasks: new Set(correctSubmissions.map((submission) => `${submission.userId}:${submission.taskId}`)).size,
    attempts: runtime.submissions.filter((submission) => submission.seminarId === seminar.id).length,
    events: runtime.eventLogs.filter((event) => event.seminarId === seminar.id).length,
    activeStudents: activeStudents.length,
  }
}

const buildFreestyleChallenge = (template: DBTemplate): PlaygroundChallenge => ({
  id: `playground-freeform-${template.id}`,
  templateId: template.id,
  title: `Freeform: ${template.title}`,
  description: 'Свободная практика без заранее заданной автопроверки. Можно исследовать схему и выполнять запросы.',
  difficulty: template.level,
  topic: 'Freeform',
  constructs: ['SELECT'],
  datasetIds: template.datasets.map((dataset) => dataset.id),
  starterSql: 'SELECT 1 AS ready;',
  expectedQuery: 'SELECT 1 AS ready;',
  feedbackMode: 'full',
})

const hasChallengeDefinitions = (templateId: string, playgroundChallenges: PlaygroundChallenge[]) =>
  playgroundChallenges.some((challenge) => challenge.templateId === templateId)

const getTemplateChallenges = (template: DBTemplate, playgroundChallenges: PlaygroundChallenge[]) => {
  const baseChallenges = playgroundChallenges.filter((challenge) => challenge.templateId === template.id)
  return baseChallenges.length > 0 ? baseChallenges : [buildFreestyleChallenge(template)]
}

const getTemplateById = (templateId: string, templates: DBTemplate[]) =>
  templates.find((template) => template.id === templateId)

const getChallengeById = (
  challengeId: string,
  templates: DBTemplate[],
  playgroundChallenges: PlaygroundChallenge[],
) => {
  const found = playgroundChallenges.find((challenge) => challenge.id === challengeId)
  if (found) {
    return found
  }

  const freestyleTemplateId = challengeId.replace('playground-freeform-', '')
  const template = templates.find((item) => item.id === freestyleTemplateId)
  return template ? buildFreestyleChallenge(template) : undefined
}

const createSeminarCsvRows = ({
  submissions,
  seminar,
  tasks,
}: {
  submissions: Submission[]
  seminar: Seminar
  tasks: TaskDefinition[]
}) =>
  submissions
    .filter((submission) => submission.seminarId === seminar.id)
    .map((submission) => {
      const user = getUser(submission.userId)
      const task = getTaskById(submission.taskId, tasks)
      return [
        user?.fullName ?? submission.userId,
        user?.login ?? '',
        task?.title ?? submission.taskId,
        submission.status,
        submission.executionTimeMs,
        submission.submittedAt,
        submission.validationDetails.summary,
        submission.sqlText,
      ]
    })

const computeTaskAnalytics = ({
  seminar,
  submissions,
  tasks,
}: {
  seminar: Seminar
  submissions: Submission[]
  tasks: TaskDefinition[]
}) =>
  seminar.taskIds.map((taskId) => {
    const taskSubmissions = submissions.filter((submission) => submission.seminarId === seminar.id && submission.taskId === taskId)
    const correct = taskSubmissions.filter((submission) => submission.status === 'correct')
    const firstCorrectAt = correct.map((submission) => submission.submittedAt).sort()[0]
    const avgTime = correct.length > 0
      ? Math.round(correct.reduce((accumulator, submission) => accumulator + submission.executionTimeMs, 0) / correct.length)
      : 0

    return {
      taskId,
      taskTitle: getTaskById(taskId, tasks)?.title ?? taskId,
      attempts: taskSubmissions.length,
      correctCount: correct.length,
      firstCorrectAt,
      avgTime,
    }
  })

const CatLogo = ({ compact = false }: { compact?: boolean }) => (
  <div className={clsx('cat-logo', compact && 'is-compact')}>
    <img src={ksitLogo} alt="KSiT" />
  </div>
)

const LoginScreen = ({
  onTeacherLogin,
  onStudentLogin,
  onResetError,
  loading,
  error,
}: {
  onTeacherLogin: (login: string, password: string) => Promise<void>
  onStudentLogin: (surname: string) => Promise<void>
  onResetError: () => void
  loading: boolean
  error: string | null
}) => {
  const [mode, setMode] = useState<'teacher' | 'student'>('student')
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [surname, setSurname] = useState('')
  const switchMode = (nextMode: 'teacher' | 'student') => {
    setMode(nextMode)
    onResetError()
  }

  return (
    <div className="login-page">
      <section className="auth-card">
        <div className="brand-row">
          <CatLogo />
          <div>
            <div className="eyebrow">Образовательная платформа</div>
            <h1>Игровая площадка ИВТ</h1>
          </div>
        </div>
        <div className="eyebrow">Вход в семинар</div>
        <p>
          Выберите режим входа и подключитесь к текущему семинару.
        </p>

        <div className="auth-role-grid">
          <button
            className={clsx('auth-role-card', mode === 'student' && 'is-active')}
            onClick={() => switchMode('student')}
            type="button"
          >
            <strong>Студент</strong>
            <span>Вход в открытую сессию по фамилии.</span>
          </button>
          <button
            className={clsx('auth-role-card', mode === 'teacher' && 'is-active')}
            onClick={() => switchMode('teacher')}
            type="button"
          >
            <strong>Преподаватель</strong>
            <span>Управление семинаром, задачами и результатами группы.</span>
          </button>
        </div>

        {mode === 'teacher' ? (
          <div className="form-grid">
            <label className="input-field">
              <span>Логин</span>
              <input
                value={login}
                onChange={(event) => {
                  setLogin(event.target.value)
                  onResetError()
                }}
                autoComplete="username"
              />
            </label>
            <label className="input-field">
              <span>Пароль</span>
              <input
                type="password"
                value={password}
                onChange={(event) => {
                  setPassword(event.target.value)
                  onResetError()
                }}
                autoComplete="current-password"
              />
            </label>
          </div>
        ) : (
          <div className="form-grid">
            <label className="input-field full-span">
              <span>Фамилия</span>
              <input
                value={surname}
                onChange={(event) => {
                  setSurname(event.target.value)
                  onResetError()
                }}
              />
            </label>
          </div>
        )}

        {error && <div className="alert danger">{error}</div>}

        <button
          className="primary-button wide-button"
          onClick={() =>
            void (mode === 'teacher'
              ? onTeacherLogin(login, password)
              : onStudentLogin(surname))}
          disabled={loading}
        >
          {loading ? 'Проверяем доступ...' : mode === 'teacher' ? 'Войти как преподаватель' : 'Войти в сессию'}
        </button>
      </section>
    </div>
  )
}

const Shell = ({
  currentUser,
  runtime,
  seminar,
  onLogout,
  engineReady,
  serverError,
  children,
}: {
  currentUser: User
  runtime: PlatformRuntime
  seminar: Seminar
  onLogout: () => void
  engineReady: boolean
  serverError: string | null
  children: React.ReactNode
}) => {
  const coverage = computeCoverageMetrics(seminar, runtime)
  const navItems = currentUser.role === 'student'
    ? [
        { to: '/seminar', label: 'Семинар' },
        { to: '/playground', label: 'Playground' },
      ]
    : [
        { to: '/overview', label: 'Обзор' },
        { to: '/seminar', label: 'Семинар' },
        { to: '/teacher', label: 'Преподаватель' },
        { to: '/playground', label: 'Playground' },
        { to: '/admin', label: 'Admin' },
      ]

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand-block">
        <div className="brand-row">
          <CatLogo compact />
          <div>
            <div className="eyebrow">Образовательная платформа</div>
            <h1>Игровая площадка ИВТ</h1>
          </div>
        </div>
          <div className="hero-meta">
            <span className="tiny-pill">{seminar.title}</span>
            <span className="tiny-pill">{seminarStatusLabel[seminar.status]}</span>
          </div>
        </div>

        {currentUser.role !== 'student' && (
          <div className="hero-metrics">
            <div className="metric-card">
              <span>Статус backend</span>
              <strong>{engineReady ? 'API online' : 'Нет связи'}</strong>
            </div>
            <div className="metric-card">
              <span>Активных студентов</span>
              <strong>{coverage.activeStudents} / 35</strong>
            </div>
            <div className="metric-card">
              <span>Попыток</span>
              <strong>{coverage.attempts}</strong>
            </div>
            <div className="metric-card">
              <span>Session</span>
              <strong>{runtime.sessionId}</strong>
            </div>
          </div>
        )}
      </header>

      <div className="toolbar">
        <nav className="route-tabs">
          {navItems.map((item) => (
            <NavLink key={item.to} to={item.to}>{item.label}</NavLink>
          ))}
        </nav>

        <div className="toolbar-actions">
          <div className="current-user-chip">
            <span>{roleLabel[currentUser.role]}</span>
            <strong>{currentUser.fullName}</strong>
          </div>
          <button className="ghost-button" onClick={onLogout}>
            Выйти
          </button>
        </div>
      </div>

      <main className="page-content">
        {serverError && <div className="alert danger">{serverError}</div>}
        {children}
      </main>
    </div>
  )
}

const OverviewPage = ({
  runtime,
  seminar,
  seminars,
  onSelectSeminar,
}: {
  runtime: PlatformRuntime
  seminar: Seminar
  seminars: Seminar[]
  onSelectSeminar: (seminarId: string) => void
}) => {
  const navigate = useNavigate()
  const leaderboard = computeLeaderboard(seminar, runtime.submissions)
  const metrics = computeCoverageMetrics(seminar, runtime)

  return (
    <div className="page-grid">
      <section className="panel highlight-panel current-seminar-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Текущий семинар</div>
            <h2>{seminar.title}</h2>
            <p>{seminar.description}</p>
          </div>
          <span className={clsx('status-pill', seminar.status === 'live' && 'is-live')}>
            {seminarStatusLabel[seminar.status]}
          </span>
        </div>

        <div className="control-row overview-actions">
          <button
            className="primary-button"
            onClick={() => {
              onSelectSeminar(seminar.id)
              navigate('/teacher')
            }}
            type="button"
          >
            Открыть управление
          </button>
          <button
            className="ghost-button"
            onClick={() => {
              onSelectSeminar(seminar.id)
              navigate('/teacher')
            }}
            type="button"
          >
            Настроить задачи
          </button>
          <button
            className="ghost-button"
            onClick={() => {
              onSelectSeminar(seminar.id)
              navigate('/seminar')
            }}
            type="button"
          >
            Открыть как студент
          </button>
        </div>

        <div className="module-grid overview-metrics">
          {[
            { label: 'Старт', value: formatDateTime(seminar.startTime) },
            { label: 'Задач', value: String(seminar.taskIds.length) },
            { label: 'Студентов', value: String(seminar.studentIds.length) },
            { label: 'Попыток', value: String(metrics.attempts) },
            { label: 'Событий', value: String(metrics.events) },
          ].map((item) => (
            <article key={item.label} className="mini-card">
              <span className="dot" />
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </article>
          ))}
        </div>
      </section>

      <section className="panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Seminars</div>
            <h2>Каталог семинаров</h2>
          </div>
        </div>
        <div className="seminar-catalog">
          {seminars.map((item) => (
            <article key={item.id} className="seminar-card">
              <div className="task-card-top">
                <span>{seminarStatusLabel[item.status]}</span>
                <span className={`badge badge-${item.status === 'live' ? 'success' : item.status === 'scheduled' ? 'neutral' : 'warning'}`}>
                  {item.accessCode}
                </span>
              </div>
              <strong>{item.title}</strong>
              <p>{item.description}</p>
              <div className="tag-row">
                <span className="tiny-pill">{formatDateTime(item.startTime)}</span>
                <span className="tiny-pill">{item.studentIds.length} студентов</span>
              </div>
              <div className="seminar-card-actions">
                <button
                  className="ghost-button"
                  onClick={() => {
                    onSelectSeminar(item.id)
                    navigate('/teacher')
                  }}
                  type="button"
                >
                  Настроить семинар
                </button>
                <button
                  className="primary-button"
                  onClick={() => {
                    onSelectSeminar(item.id)
                    navigate('/teacher')
                  }}
                  type="button"
                >
                  Настроить задачи
                </button>
              </div>
            </article>
          ))}
        </div>
      </section>

      <section className="panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Снимок семинара</div>
            <h2>Операционные метрики</h2>
          </div>
        </div>
        <div className="stats-row">
          <div className="stat-box">
            <span>Активных студентов</span>
            <strong>{metrics.activeStudents} / 35 target</strong>
          </div>
          <div className="stat-box">
            <span>Попыток</span>
            <strong>{metrics.attempts}</strong>
          </div>
          <div className="stat-box">
            <span>Событий в аудите</span>
            <strong>{metrics.events}</strong>
          </div>
        </div>
      </section>

      <section className="panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Top learners</div>
            <h2>Лидерборд</h2>
          </div>
        </div>
        <div className="leaderboard-list">
          {leaderboard.slice(0, 3).map((entry) => (
            <div key={entry.user.id} className="leader-row">
              <div className="leader-rank">#{entry.rank}</div>
              <div>
                <strong>{entry.user.fullName}</strong>
                <p>{entry.solvedCount} решено · {entry.attemptsCount} попыток</p>
              </div>
            </div>
          ))}
        </div>
      </section>
    </div>
  )
}

const ResultGrid = ({ result }: { result?: QueryResultTable }) => {
  if (!result) {
    return <div className="empty-state">Результат запроса появится здесь после выполнения.</div>
  }

  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {result.columns.map((column) => (
              <th key={column}>{column}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {result.rows.map((row, rowIndex) => (
            <tr key={`row-${rowIndex}`}>
              {row.map((cell, columnIndex) => (
                <td key={`cell-${rowIndex}-${columnIndex}`}>{cell === null ? 'NULL' : String(cell)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

const SchemaDiagram = ({ template }: { template: DBTemplate }) => {
  const tableWidth = 272
  const boardPadding = 32
  const headerHeight = 52
  const rowHeight = 56
  const boardRef = useRef<HTMLDivElement | null>(null)
  const [positions, setPositions] = useState<Record<string, { x: number, y: number }>>(
    () =>
      Object.fromEntries(
        template.tables.map((table) => [
          table.name,
          {
            x: table.position.x,
            y: table.position.y,
          },
        ]),
      ),
  )
  const [dragState, setDragState] = useState<{
    tableName: string
    offsetX: number
    offsetY: number
  } | null>(null)

  useEffect(() => {
    if (!dragState) {
      return
    }

    const handleMouseMove = (event: MouseEvent) => {
      const board = boardRef.current
      if (!board) {
        return
      }

      const rect = board.getBoundingClientRect()
      const x = event.clientX - rect.left + board.scrollLeft - dragState.offsetX
      const y = event.clientY - rect.top + board.scrollTop - dragState.offsetY

      setPositions((previous) => ({
        ...previous,
        [dragState.tableName]: {
          x: Math.max(boardPadding, Math.round(x)),
          y: Math.max(boardPadding, Math.round(y)),
        },
      }))
    }

    const handleMouseUp = () => {
      setDragState(null)
    }

    window.addEventListener('mousemove', handleMouseMove)
    window.addEventListener('mouseup', handleMouseUp)
    return () => {
      window.removeEventListener('mousemove', handleMouseMove)
      window.removeEventListener('mouseup', handleMouseUp)
    }
  }, [boardPadding, dragState])

  const diagramTables = template.tables.map((table) => ({
    ...table,
    position: positions[table.name] ?? table.position,
  }))

  const relations = diagramTables.flatMap((table) =>
    table.columns.flatMap((column, columnIndex) => {
      if (!column.references) {
        return []
      }

      const target = diagramTables.find((candidate) => candidate.name === column.references?.table)
      if (!target) {
        return []
      }

      const sourceX = target.position.x < table.position.x ? table.position.x : table.position.x + tableWidth
      const targetX = target.position.x < table.position.x ? target.position.x + tableWidth : target.position.x
      const sourceY = table.position.y + headerHeight + columnIndex * rowHeight + rowHeight / 2
      const targetColumnIndex = target.columns.findIndex((candidate) => candidate.name === column.references?.column)
      const targetY = target.position.y + headerHeight + Math.max(targetColumnIndex, 0) * rowHeight + rowHeight / 2

      return [{
        id: `${table.name}.${column.name}->${target.name}.${column.references.column}`,
        fromX: sourceX,
        fromY: sourceY,
        toX: targetX,
        toY: targetY,
      }]
    }),
  )

  const boardWidth = Math.max(
    1400,
    ...diagramTables.map((table) => table.position.x + tableWidth + boardPadding),
  )
  const boardHeight = Math.max(
    720,
    ...diagramTables.map((table) => table.position.y + headerHeight + table.columns.length * rowHeight + boardPadding),
  )

  return (
    <div ref={boardRef} className="diagram-board">
      <div className="diagram-canvas" style={{ width: boardWidth, height: boardHeight }}>
        <svg className="diagram-relations" width={boardWidth} height={boardHeight} aria-hidden="true">
          <defs>
            <marker
              id="diagram-arrow"
              markerWidth="10"
              markerHeight="10"
              refX="9"
              refY="5"
              orient="auto"
            >
              <path d="M0 0L10 5L0 10Z" fill="#94a3b8" />
            </marker>
          </defs>
          {relations.map((relation) => {
            const middleX = relation.fromX + (relation.toX - relation.fromX) / 2
            const path = `M ${relation.fromX} ${relation.fromY} C ${middleX} ${relation.fromY}, ${middleX} ${relation.toY}, ${relation.toX} ${relation.toY}`
            return (
              <path
                key={relation.id}
                d={path}
                className="diagram-relation-path"
                markerEnd="url(#diagram-arrow)"
              />
            )
          })}
        </svg>

        {diagramTables.map((table) => (
          <article
            key={table.name}
            className="diagram-table"
            style={{ left: table.position.x, top: table.position.y, width: tableWidth }}
          >
            <header
              onMouseDown={(event) => {
                const rect = event.currentTarget.getBoundingClientRect()
                setDragState({
                  tableName: table.name,
                  offsetX: event.clientX - rect.left,
                  offsetY: event.clientY - rect.top,
                })
              }}
            >
              <span>{table.name}</span>
              <small>Перетащить</small>
            </header>
            <ul>
              {table.columns.map((column) => (
                <li key={column.name}>
                  <strong>{column.name}</strong>
                  <span>{column.type}</span>
                  {column.references && (
                    <em>
                      → {column.references.table}.{column.references.column}
                    </em>
                  )}
                </li>
              ))}
            </ul>
          </article>
        ))}
      </div>
    </div>
  )
}

const SchemaPanel = ({
  template,
  mode,
}: {
  template: DBTemplate
  mode: 'schema' | 'diagram' | 'examples'
}) => {
  if (mode === 'diagram') {
    return <SchemaDiagram key={template.id} template={template} />
  }

  if (mode === 'examples') {
    return (
      <div className="schema-cards">
        {template.tables.map((table) => (
          <article key={table.name} className="schema-card">
            <div className="schema-card-header">
              <h3>{table.name}</h3>
              <p>{table.description}</p>
            </div>
            <div className="mini-table">
              <table>
                <thead>
                  <tr>
                    {table.columns.map((column) => (
                      <th key={column.name}>{column.name}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {table.sampleRows.length > 0 ? (
                    table.sampleRows.map((row, rowIndex) => (
                      <tr key={`${table.name}-${rowIndex}`}>
                        {row.map((cell, columnIndex) => (
                          <td key={`${table.name}-${rowIndex}-${columnIndex}`}>{cell}</td>
                        ))}
                      </tr>
                    ))
                  ) : (
                    <tr>
                      <td colSpan={table.columns.length}>Примеры данных не заданы.</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </article>
        ))}
      </div>
    )
  }

  return (
    <div className="schema-cards">
      {template.tables.map((table) => (
        <article key={table.name} className="schema-card">
          <div className="schema-card-header">
            <h3>{table.name}</h3>
            <p>{table.description}</p>
          </div>
          <ul className="column-list">
            {table.columns.map((column) => (
              <li key={column.name}>
                <div>
                  <strong>{column.name}</strong>
                  {column.isPrimaryKey && <span className="tiny-pill">PK</span>}
                </div>
                <span>{column.type}</span>
                {column.references && (
                  <small>
                    FK → {column.references.table}.{column.references.column}
                  </small>
                )}
              </li>
            ))}
          </ul>
        </article>
      ))}
    </div>
  )
}

const SqlWorkspace = ({
  title,
  subtitle,
  sqlValue,
  onSqlChange,
  onExecute,
  onSubmit,
  onReset,
  executeDisabled,
  submitDisabled,
  executePending,
  submitPending,
  submitHint,
}: {
  title: string
  subtitle: string
  sqlValue: string
  onSqlChange: (value: string) => void
  onExecute: () => Promise<void> | void
  onSubmit?: () => Promise<void> | void
  onReset: () => void
  executeDisabled?: boolean
  submitDisabled?: boolean
  executePending?: boolean
  submitPending?: boolean
  submitHint?: string
}) => (
  <section className="panel workspace-panel">
    <div className="section-header">
      <div>
        <div className="eyebrow">Редактор SQL</div>
        <h2>{title}</h2>
        <p>{subtitle}</p>
      </div>
    </div>

    <div className="editor-shell">
      <CodeMirror
        value={sqlValue}
        height="360px"
        extensions={[sql()]}
        onChange={onSqlChange}
        basicSetup={{
          lineNumbers: true,
          bracketMatching: true,
          autocompletion: true,
        }}
      />
    </div>

    {(executePending || submitPending) && (
      <div className="alert neutral">
        {executePending && 'Запрос выполняется. Дождитесь результата.'}
        {submitPending && 'Решение отправлено на проверку. Выполняем валидацию.'}
      </div>
    )}

    <div className="editor-actions">
      <button className="primary-button" onClick={() => void onExecute()} disabled={executeDisabled || executePending || submitPending}>
        {executePending ? 'Выполняем...' : 'Выполнить'}
      </button>
      {onSubmit && (
        <button className="secondary-button" onClick={() => void onSubmit()} disabled={submitDisabled || executePending || submitPending}>
          {submitPending ? 'Проверяем решение...' : 'Проверить решение'}
        </button>
      )}
      <button className="ghost-button" onClick={onReset} disabled={executePending || submitPending}>
        Сбросить черновик
      </button>
    </div>

    {submitHint && <p className="muted-text">{submitHint}</p>}
  </section>
)

const DraftSqlEditor = ({
  storageKey,
  initialSql,
  title,
  subtitle,
  onSaveDraft,
  onExecute,
  onSubmit,
  executeDisabled,
  submitDisabled,
  submitHint,
}: {
  storageKey: string
  initialSql: string
  title: string
  subtitle: string
  onSaveDraft: (key: string, value: string) => void
  onExecute: (sqlText: string) => void
  onSubmit?: (sqlText: string) => void
  executeDisabled?: boolean
  submitDisabled?: boolean
  submitHint?: string
}) => {
  const [localSql, setLocalSql] = useState(initialSql)
  const [executePending, setExecutePending] = useState(false)
  const [submitPending, setSubmitPending] = useState(false)
  const persistDraft = useEffectEvent((nextValue: string) => onSaveDraft(storageKey, nextValue))

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      persistDraft(localSql)
    }, 200)

    return () => window.clearTimeout(timeout)
  }, [localSql])

  return (
    <SqlWorkspace
      title={title}
      subtitle={subtitle}
      sqlValue={localSql}
      onSqlChange={setLocalSql}
      onExecute={async () => {
        setExecutePending(true)
        try {
          await onExecute(localSql)
        } finally {
          setExecutePending(false)
        }
      }}
      onSubmit={onSubmit ? async () => {
        setSubmitPending(true)
        try {
          await onSubmit(localSql)
        } finally {
          setSubmitPending(false)
        }
      } : undefined}
      onReset={() => setLocalSql(initialSql)}
      executeDisabled={executeDisabled}
      submitDisabled={submitDisabled}
      executePending={executePending}
      submitPending={submitPending}
      submitHint={submitHint}
    />
  )
}

const StudentWorkspace = ({
  currentUser,
  runtime,
  seminar,
  seminars,
  tasks,
  templates,
  onSelectTask,
  onSelectSeminar,
  onSaveDraft,
  onRunSeminarQuery,
  onSubmitSeminarQuery,
}: {
  currentUser: User
  runtime: PlatformRuntime
  seminar: Seminar
  seminars: Seminar[]
  tasks: TaskDefinition[]
  templates: DBTemplate[]
  onSelectTask: (taskId: string) => void
  onSelectSeminar: (seminarId: string) => void
  onSaveDraft: (key: string, value: string) => void
  onRunSeminarQuery: (task: TaskDefinition, sqlText: string) => Promise<void>
  onSubmitSeminarQuery: (task: TaskDefinition, sqlText: string) => Promise<void>
}) => {
  const [schemaOpen, setSchemaOpen] = useState(false)
  const [schemaTab, setSchemaTab] = useState<'schema' | 'diagram' | 'examples'>('diagram')
  const availableSeminars = currentUser.role === 'student'
    ? seminars
      .filter((item) => item.status === 'live')
      .filter((item) => item.studentIds.includes(currentUser.id))
    : seminars
  const selectedTaskId = runtime.selectedTaskByUser[currentUser.id] ?? seminar.taskIds[0]
  const task = getTaskById(selectedTaskId, tasks) ?? tasks[0]
  const taskTemplate = getTemplateById(task?.templateId ?? seminar.templateId, templates) ?? templates[0]

  const storageKey = draftKey(currentUser.id, 'seminar', task.id)
  const initialDraft = runtime.drafts[storageKey] ?? ''
  const latestSubmission = pickLatestByTask(runtime.submissions, currentUser.id, task.id, seminar.id)
  const latestRun = pickLatestRun(runtime.queryRuns, currentUser.id, 'seminar', task.id, seminar.id)
  const submitHint = seminar.status !== 'live'
    ? 'Проверка станет доступна после открытия семинара преподавателем.'
    : seminar.settings.submissionsFrozen
      ? 'Преподаватель временно заморозил отправки по этому семинару.'
      : !seminar.settings.autoValidationEnabled
        ? 'Автопроверка для этого семинара сейчас выключена.'
        : undefined

  if (availableSeminars.length === 0) {
    return (
      <section className="panel student-empty-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Семинар</div>
            <h2>{currentUser.role === 'student' ? 'Нет открытых семинаров' : 'Нет доступных семинаров'}</h2>
          </div>
        </div>
        <p className="muted-text">
          {currentUser.role === 'student'
            ? 'Когда преподаватель откроет занятие, здесь появится список доступных семинаров.'
            : 'После создания или выбора семинара здесь появится рабочее пространство выбранной сессии.'}
        </p>
      </section>
    )
  }

  return (
    <div className="student-layout">
      <section className="panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">{currentUser.role === 'student' ? 'Открытые семинары' : 'Семинары'}</div>
            <h2>Выбор семинара</h2>
            <p>
              {currentUser.role === 'student'
                ? 'Студент видит только активные занятия, к которым открыт доступ.'
                : 'Преподаватель может открыть любой семинар из доступного каталога и перейти в его рабочее пространство.'}
            </p>
          </div>
        </div>
        <div className="seminar-selector-grid">
          {availableSeminars.map((item) => (
            <button
              key={item.id}
              className={clsx('seminar-selector-card', seminar.id === item.id && 'is-active')}
              onClick={() => onSelectSeminar(item.id)}
            >
              <div className="task-card-top">
                <span>{seminarStatusLabel[item.status]}</span>
                {currentUser.role !== 'student' && <span className="badge badge-success">{item.accessCode}</span>}
              </div>
              <strong>{item.title}</strong>
              <p>{item.description}</p>
            </button>
          ))}
        </div>
      </section>

      <section className="panel task-brief">
        <div className="section-header">
          <div>
            <div className="eyebrow">Текущий семинар</div>
            <h2>{seminar.title}</h2>
            <p>{task.description}</p>
          </div>
          <div className="task-meta">
            <span className={clsx('status-pill', seminar.status === 'live' && 'is-live')}>
              {seminarStatusLabel[seminar.status]}
            </span>
            {seminar.settings.submissionsFrozen && <span className="tiny-pill warning">Отправки заморожены</span>}
            <span className="tiny-pill">{task.difficulty}</span>
            <span className="tiny-pill">{task.taskType}</span>
            {taskTemplate && (
              <button
                className="ghost-button"
                onClick={() => setSchemaOpen((previous) => !previous)}
                type="button"
              >
                {schemaOpen ? 'Скрыть схему БД' : 'Схема БД'}
              </button>
            )}
          </div>
        </div>
        <div className="task-switcher">
          {seminar.taskIds.map((taskId, index) => {
            const seminarTask = getTaskById(taskId, tasks)
            if (!seminarTask) {
              return null
            }

            const submission = pickLatestByTask(runtime.submissions, currentUser.id, taskId, seminar.id)

            return (
              <button
                key={taskId}
                className={clsx('task-card task-card-compact', selectedTaskId === taskId && 'is-active')}
                onClick={() => onSelectTask(taskId)}
              >
                <div className="task-card-top">
                  <span>Задача {index + 1}</span>
                  <span className={`badge badge-${queryStatusTone(submission?.status)}`}>
                    {queryStatusLabel(submission?.status ?? 'new')}
                  </span>
                </div>
                <strong>{seminarTask.title}</strong>
              </button>
            )
          })}
        </div>
      </section>

      {schemaOpen && taskTemplate && (
        <section className="panel full-width-panel">
          <div className="section-header">
            <div>
              <div className="eyebrow">Schema view</div>
              <h2>Схема базы данных</h2>
              <p>Структура таблиц и связей для текущего задания.</p>
            </div>
          </div>
          <div className="tab-row">
            {(['schema', 'diagram', 'examples'] as const).map((tab) => (
              <button
                key={tab}
                className={clsx('tab-button', schemaTab === tab && 'is-active')}
                onClick={() => setSchemaTab(tab)}
                type="button"
              >
                {tab}
              </button>
            ))}
          </div>
          <SchemaPanel template={taskTemplate} mode={schemaTab} />
        </section>
      )}

      <DraftSqlEditor
        key={storageKey}
        storageKey={storageKey}
        initialSql={initialDraft}
        title={task.title}
        subtitle="Только условие, поле ввода и вывод результата. Эталон скрыт, пока преподаватель не откроет его для группы."
        onSaveDraft={onSaveDraft}
        onExecute={(sqlText) => void onRunSeminarQuery(task, sqlText)}
        onSubmit={(sqlText) => void onSubmitSeminarQuery(task, sqlText)}
        executeDisabled={seminar.status !== 'live'}
        submitDisabled={
          seminar.status !== 'live'
          || seminar.settings.submissionsFrozen
          || !seminar.settings.autoValidationEnabled
        }
        submitHint={submitHint}
      />

      <section className="panel">
        <div className="result-banner">
          <div>
            <div className="eyebrow">Последний запуск</div>
            <h2>Результат выполнения</h2>
          </div>
          {latestRun && <span className={`badge badge-${queryStatusTone(latestRun.status)}`}>{queryStatusLabel(latestRun.status)}</span>}
        </div>

        {latestRun?.errorMessage && <div className="alert danger">{localizeUiText(latestRun.errorMessage)}</div>}

        {latestSubmission && (
          <div className={`alert ${queryStatusTone(latestSubmission.status)}`}>
            <strong>{queryStatusLabel(latestSubmission.status)}</strong>
            <p>
              {seminar.settings.diagnosticsVisible
                ? localizeUiText(latestSubmission.validationDetails.summary)
                : latestSubmission.status === 'correct'
                  ? 'Решение принято.'
                  : 'Решение не принято.'}
            </p>
            {seminar.settings.diagnosticsVisible && (
              <div className="dataset-outcomes">
                {latestSubmission.validationDetails.datasets.map((dataset) => (
                  <div key={dataset.datasetId} className="dataset-card">
                    <strong>{dataset.datasetId}</strong>
                    <p>{localizeUiText(dataset.message)}</p>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        <ResultGrid result={latestRun?.result} />
      </section>

      {seminar.settings.referenceSolutionVisible && task.expectedQuery && (
        <section className="panel">
          <div className="section-header">
            <div>
              <div className="eyebrow">Эталон открыт преподавателем</div>
              <h2>Референсное решение</h2>
            </div>
          </div>
          <pre className="sql-preview">{task.expectedQuery}</pre>
        </section>
      )}
    </div>
  )
}

const TeacherDashboard = ({
  currentUser,
  runtime,
  seminar,
  seminars,
  tasks,
  templates,
  onToggleSetting,
  onToggleSeminarStatus,
  onSetSeminarStatus,
  onSelectSeminar,
  onPickStudent,
  onSaveSeminarMeta,
  onCreateSeminar,
  onCreateTask,
  onAssignTask,
  onRemoveTask,
}: {
  currentUser: User
  runtime: PlatformRuntime
  seminar: Seminar
  seminars: Seminar[]
  tasks: TaskDefinition[]
  templates: DBTemplate[]
  onToggleSetting: (setting: keyof Seminar['settings']) => void
  onToggleSeminarStatus: () => void
  onSetSeminarStatus: (seminarId: string, targetStatus: Seminar['status']) => void
  onSelectSeminar: (seminarId: string) => void
  onPickStudent: () => void
  onSaveSeminarMeta: (field: keyof SeminarMetaOverride, value: string) => void
  onCreateSeminar: (payload: {
    title: string
    description: string
    templateId: string
    startTime: string
  }) => void
  onCreateTask: (payload: {
    title: string
    description: string
    templateId: string
    difficulty: TaskDefinition['difficulty']
    taskType: string
    constructsText: string
    starterSql: string
    expectedQuery: string
    hintsText: string
    datasetIds: string[]
  }) => void
  onAssignTask: (taskId: string) => void
  onRemoveTask: (taskId: string) => void
}) => {
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<'all' | Submission['status']>('all')
  const [taskFilter, setTaskFilter] = useState<'all' | string>('all')
  const [focusedTaskId, setFocusedTaskId] = useState(seminar.taskIds[0] ?? '')
  const [schemaTab, setSchemaTab] = useState<'schema' | 'diagram' | 'examples'>('diagram')
  const [schemaExpanded, setSchemaExpanded] = useState(true)
  const [showSeminarCatalog, setShowSeminarCatalog] = useState(false)
  const [seminarSetupOpen, setSeminarSetupOpen] = useState(false)
  const [seminarCreateOpen, setSeminarCreateOpen] = useState(false)
  const deferredSearch = useDeferredValue(search)
  const leaderboard = computeLeaderboard(seminar, runtime.submissions)
  const metrics = computeCoverageMetrics(seminar, runtime)

  const [newSeminarTitle, setNewSeminarTitle] = useState('Новый семинар SQL')
  const [newSeminarDescription, setNewSeminarDescription] = useState('Локально созданный семинар для следующего потока.')
  const [newSeminarTemplateId, setNewSeminarTemplateId] = useState(templates[0]?.id ?? '')
  const [newSeminarStart, setNewSeminarStart] = useState('2026-04-07T09:30')
  const [newTaskTitle, setNewTaskTitle] = useState('Новая задача на семинар')
  const [newTaskDescription, setNewTaskDescription] = useState('Опишите ожидаемый результат и ограничения проверки.')
  const [newTaskTemplateId, setNewTaskTemplateId] = useState(templates[0]?.id ?? '')
  const [newTaskDifficulty, setNewTaskDifficulty] = useState<TaskDefinition['difficulty']>('medium')
  const [newTaskType, setNewTaskType] = useState('SELECT')
  const [newTaskConstructs, setNewTaskConstructs] = useState('SELECT, JOIN')
  const [newTaskStarterSql, setNewTaskStarterSql] = useState('SELECT *\nFROM orders\nLIMIT 10;')
  const [newTaskExpectedQuery, setNewTaskExpectedQuery] = useState('SELECT *\nFROM orders\nLIMIT 10;')
  const [newTaskHints, setNewTaskHints] = useState('Используйте только учебную схему.\nПроверьте результат на всех датасетах.')
  const [selectedDatasetIds, setSelectedDatasetIds] = useState<string[]>(templates[0]?.datasets.map((dataset) => dataset.id) ?? [])
  const [taskDialogOpen, setTaskDialogOpen] = useState(false)
  const availableTasks = tasks.filter((task) => !seminar.taskIds.includes(task.id))
  const taskTemplate = getTemplateById(newTaskTemplateId, templates) ?? templates[0]
  const activeFocusedTaskId = seminar.taskIds.includes(focusedTaskId) ? focusedTaskId : (seminar.taskIds[0] ?? '')
  const focusedTask = getTaskById(activeFocusedTaskId, tasks) ?? getTaskById(seminar.taskIds[0], tasks) ?? tasks[0]
  const focusedTemplate = getTemplateById(focusedTask?.templateId ?? seminar.templateId, templates) ?? templates[0]

  if (currentUser.role === 'student') {
    return <div className="panel restricted-panel">Доступ к панели преподавателя открыт только ролям `teacher` и `admin`.</div>
  }

  const filteredStudents = seminar.studentIds
    .map((studentId) => getUser(studentId))
    .filter((user): user is User => Boolean(user))
    .filter((user) => user.fullName.toLowerCase().includes(deferredSearch.toLowerCase()))

  const filteredSubmissions = [...runtime.submissions]
    .filter((submission) => submission.seminarId === seminar.id)
    .filter((submission) => {
      if (statusFilter !== 'all' && submission.status !== statusFilter) {
        return false
      }

      if (taskFilter !== 'all' && submission.taskId !== taskFilter) {
        return false
      }

      const user = getUser(submission.userId)
      return user?.fullName.toLowerCase().includes(deferredSearch.toLowerCase()) ?? false
    })
    .sort((left, right) => right.submittedAt.localeCompare(left.submittedAt))
  const analytics = computeTaskAnalytics({ seminar, submissions: runtime.submissions, tasks })
  const focusedTaskSubmissions = focusedTask
    ? filteredSubmissions.filter((submission) => submission.taskId === focusedTask.id).slice(0, 6)
    : []
  const handleCreateTask = () => {
    onCreateTask({
      title: newTaskTitle,
      description: newTaskDescription,
      templateId: newTaskTemplateId,
      difficulty: newTaskDifficulty,
      taskType: newTaskType,
      constructsText: newTaskConstructs,
      starterSql: newTaskStarterSql,
      expectedQuery: newTaskExpectedQuery,
      hintsText: newTaskHints,
      datasetIds: selectedDatasetIds,
    })
    setTaskDialogOpen(false)
  }

  return (
    <div className="teacher-dashboard">
      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Seminars</div>
            <h2>Семинары</h2>
            <p>Откройте каталог, отредактируйте текущий семинар или создайте новый через отдельные действия.</p>
          </div>
          <div className="control-row section-header-actions">
            <button className="ghost-button" onClick={() => setShowSeminarCatalog((previous) => !previous)} type="button">
              {showSeminarCatalog ? 'Скрыть все семинары' : 'Просмотреть все семинары'}
            </button>
            <button className="ghost-button" onClick={() => setSeminarSetupOpen(true)} type="button">
              Редактировать
            </button>
            <button className="primary-button" onClick={() => setSeminarCreateOpen(true)} type="button">
              Создать семинар
            </button>
          </div>
        </div>
        <div className="teacher-control-nest">
          <div className="alert neutral">
            Текущий семинар: <strong>{seminar.title}</strong>
          </div>
          <div className="tag-row">
            <span className="tiny-pill">{seminarStatusLabel[seminar.status]}</span>
            <span className="tiny-pill">Код {seminar.accessCode}</span>
            <span className="tiny-pill">{seminar.taskIds.length} задач</span>
            <span className="tiny-pill">{seminar.studentIds.length} студентов</span>
          </div>
        </div>
        {showSeminarCatalog && (
          <div className="seminar-selector-grid">
            {seminars.map((item) => (
              <article
                key={item.id}
                className={clsx('seminar-selector-card', seminar.id === item.id && 'is-active')}
              >
                <div className="task-card-top seminar-selector-header">
                  <span>{seminarStatusLabel[item.status]}</span>
                  <span className={`badge badge-${item.status === 'live' ? 'success' : item.status === 'scheduled' ? 'neutral' : 'warning'}`}>
                    {item.accessCode}
                  </span>
                </div>
                <strong>{item.title}</strong>
                <p>{item.description}</p>
                <div className="tag-row">
                  {seminar.id === item.id && <span className="tiny-pill">Открыт сейчас</span>}
                  <span className="tiny-pill">{item.taskIds.length} задач</span>
                  <span className="tiny-pill">{item.studentIds.length} студентов</span>
                </div>
                <div className="seminar-selector-actions">
                  <button
                    className={clsx(seminar.id === item.id ? 'primary-button' : 'ghost-button')}
                    onClick={() => onSelectSeminar(item.id)}
                  >
                    {seminar.id === item.id ? 'Текущий семинар' : 'Открыть'}
                  </button>
                  <button
                    className="ghost-button"
                    onClick={() => {
                      onSelectSeminar(item.id)
                      setSeminarSetupOpen(true)
                    }}
                    type="button"
                  >
                    Редактировать
                  </button>
                  {item.status === 'live' ? (
                    <button
                      className="ghost-button"
                      onClick={() => onSetSeminarStatus(item.id, 'closed')}
                      type="button"
                    >
                      Закрыть
                    </button>
                  ) : (
                    <button
                      className="ghost-button"
                      onClick={() => onSetSeminarStatus(item.id, 'live')}
                      type="button"
                    >
                      {item.status === 'scheduled' ? 'Начать' : 'Переоткрыть'}
                    </button>
                  )}
                </div>
              </article>
            ))}
          </div>
        )}
      </section>

      <section className="panel full-width-panel teacher-overview-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Live control</div>
            <h2>{seminar.title}</h2>
            <p>{seminar.description}</p>
          </div>
          <div className="task-meta">
            <span className={clsx('status-pill', seminar.status === 'live' && 'is-live')}>
              {seminarStatusLabel[seminar.status]}
            </span>
            <span className="tiny-pill">Код {seminar.accessCode}</span>
            <span className="tiny-pill">{seminar.studentIds.length} студентов</span>
          </div>
        </div>

        <div className="teacher-control-nest">
          <div className="alert neutral">
            Сейчас редактируется семинар: <strong>{seminar.title}</strong>
          </div>

          <div className="stats-row teacher-summary-row">
            <div className="stat-box">
              <span>Активных студентов</span>
              <strong>{metrics.activeStudents}</strong>
            </div>
            <div className="stat-box">
              <span>Отправок по семинару</span>
              <strong>{filteredSubmissions.length}</strong>
            </div>
            <div className="stat-box">
              <span>Событий в логе</span>
              <strong>{runtime.eventLogs.filter((event) => event.seminarId === seminar.id).length}</strong>
            </div>
          </div>

          <div className="control-row">
            <button className="primary-button" onClick={onToggleSeminarStatus}>
              {seminar.status === 'live' ? 'Закрыть доступ' : 'Открыть доступ'}
            </button>
            <button className="ghost-button" onClick={() => onToggleSetting('submissionsFrozen')}>
              {seminar.settings.submissionsFrozen ? 'Разморозить отправки' : 'Заморозить отправки'}
            </button>
            <button className="ghost-button" onClick={onPickStudent}>
              Выбрать студента к доске
            </button>
            {runtime.lastPickedStudentId && (
              <div className="tiny-pill teacher-picked-student">
                К доске: <strong>{getUser(runtime.lastPickedStudentId)?.fullName}</strong>
              </div>
            )}
            <button
              className="ghost-button"
              onClick={() =>
                downloadCsv({
                  filename: `seminar-${seminar.id}-results.csv`,
                  headers: ['student_name', 'login', 'task', 'status', 'execution_ms', 'submitted_at', 'summary', 'sql_text'],
                  rows: createSeminarCsvRows({ submissions: filteredSubmissions, seminar, tasks }),
                })}
            >
              Экспорт CSV
            </button>
            <button
              className="ghost-button"
              onClick={() =>
                downloadWorkbook({
                  filename: `seminar-${seminar.id}-report.xlsx`,
                  sheets: [
                    {
                      name: 'Summary',
                      rows: analytics.map((item) => ({
                        task: item.taskTitle,
                        attempts: item.attempts,
                        correct_count: item.correctCount,
                        first_correct_at: item.firstCorrectAt ?? '',
                        avg_time_ms: item.avgTime,
                      })),
                    },
                    {
                      name: 'Submissions',
                      rows: filteredSubmissions.map((submission) => ({
                        student_name: getUser(submission.userId)?.fullName ?? submission.userId,
                        login: getUser(submission.userId)?.login ?? '',
                        task: getTaskById(submission.taskId, tasks)?.title ?? submission.taskId,
                        status: submission.status,
                        execution_ms: submission.executionTimeMs,
                        submitted_at: submission.submittedAt,
                        summary: submission.validationDetails.summary,
                        sql_text: submission.sqlText,
                      })),
                    },
                  ],
                })}
            >
              Экспорт XLSX
            </button>
          </div>

          <div className="toggle-grid">
            {([
              ['leaderboardEnabled', 'Рейтинг'],
              ['autoValidationEnabled', 'Автопроверка'],
              ['notificationsEnabled', 'Уведомления'],
              ['diagnosticsVisible', 'Диагностика студенту'],
              ['referenceSolutionVisible', 'Показывать эталон студентам'],
            ] as Array<[keyof Seminar['settings'], string]>).map(([key, label]) => (
              <button
                key={key}
                className={clsx('toggle-card', seminar.settings[key] && 'is-on')}
                onClick={() => onToggleSetting(key)}
              >
                <span>{label}</span>
                <strong>{seminar.settings[key] ? 'ON' : 'OFF'}</strong>
              </button>
            ))}
          </div>
        </div>
      </section>

      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Schema view</div>
            <h2>Схема и диаграмма БД</h2>
            <p>Структура базы текущего семинара и примеры данных для выбранной задачи.</p>
          </div>
          <button
            className="ghost-button"
            onClick={() => setSchemaExpanded((previous) => !previous)}
            type="button"
          >
            {schemaExpanded ? 'Свернуть' : 'Развернуть'}
          </button>
        </div>

        {schemaExpanded ? (
          <>
            <div className="tab-row">
              {(['schema', 'diagram', 'examples'] as const).map((tab) => (
                <button
                  key={tab}
                  className={clsx('tab-button', schemaTab === tab && 'is-active')}
                  onClick={() => setSchemaTab(tab)}
                >
                  {tab}
                </button>
              ))}
            </div>
            <SchemaPanel template={focusedTemplate} mode={schemaTab} />
          </>
        ) : (
          <div className="tag-row">
            <span className="tiny-pill">{focusedTemplate.title}</span>
            <span className="tiny-pill">{focusedTemplate.tables.length} таблиц</span>
            <span className="tiny-pill">{focusedTemplate.datasets.length} датасетов</span>
          </div>
        )}
      </section>

      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Tasks</div>
            <h2>Задания текущего семинара</h2>
          </div>
          <button className="primary-button" onClick={() => setTaskDialogOpen(true)} type="button">
            Создать задачу
          </button>
        </div>

        <div className="task-switcher teacher-task-switcher">
          {seminar.taskIds.map((taskId) => {
            const task = getTaskById(taskId, tasks)
            if (!task) {
              return null
            }

            return (
              <button
                key={task.id}
                className={clsx('task-card task-card-compact', activeFocusedTaskId === task.id && 'is-active')}
                onClick={() => setFocusedTaskId(task.id)}
              >
                <strong>{task.title}</strong>
                <p>{task.taskType}</p>
              </button>
            )
          })}
        </div>

        {focusedTask && (
          <div className="teacher-focus-card">
            <div className="section-header">
              <div>
                <div className="eyebrow">Выбранная задача</div>
                <h3>{focusedTask.title}</h3>
                <p>{focusedTask.description}</p>
              </div>
              <button className="ghost-button" onClick={() => onRemoveTask(focusedTask.id)}>
                Убрать из семинара
              </button>
            </div>
            <div className="tag-row">
              <span className="tiny-pill">{focusedTask.taskType}</span>
              <span className="tiny-pill">{focusedTask.difficulty}</span>
              <span className="tiny-pill">{focusedTask.datasetIds.length} датасетов</span>
            </div>
            <div className="teacher-focus-grid">
              <div className="mini-card">
                <strong>Подсказки</strong>
                <div className="hint-list compact-hint-list">
                  {focusedTask.hints.map((hint) => (
                    <div key={hint} className="hint-item">
                      {hint}
                    </div>
                  ))}
                </div>
              </div>
              <div className="mini-card">
                <strong>Эталонный запрос</strong>
                <pre className="sql-preview">{focusedTask.expectedQuery}</pre>
              </div>
            </div>
            <div className="section-header">
              <div>
                <div className="eyebrow">Последние отправки</div>
                <h3>Кто сдавал эту задачу</h3>
              </div>
            </div>
            <div className="submission-review-list">
              {focusedTaskSubmissions.map((submission) => (
                <article key={submission.id} className="submission-card">
                  <div className="submission-card-top">
                    <div>
                      <strong>{getUser(submission.userId)?.fullName}</strong>
                      <p>{formatDateTime(submission.submittedAt)}</p>
                    </div>
                    <span className={`badge badge-${queryStatusTone(submission.status)}`}>{queryStatusLabel(submission.status)}</span>
                  </div>
                  <pre className="sql-preview">{submission.sqlText}</pre>
                </article>
              ))}
              {focusedTaskSubmissions.length === 0 && <div className="empty-state">По выбранной задаче пока нет отправок.</div>}
            </div>
          </div>
        )}
      </section>

      {taskDialogOpen && (
        <div className="dialog-backdrop" onClick={() => setTaskDialogOpen(false)} role="presentation">
          <section className="dialog-card task-dialog-card" onClick={(event) => event.stopPropagation()}>
            <div className="section-header">
              <div>
                <div className="eyebrow">Authoring</div>
                <h2>Создать новую задачу</h2>
                <p>Новая задача сразу попадёт в общий каталог и будет добавлена в текущий семинар.</p>
              </div>
              <button className="ghost-button" onClick={() => setTaskDialogOpen(false)} type="button">
                Закрыть
              </button>
            </div>

            <div className="task-dialog-layout">
              <div className="task-dialog-form">
                <div className="form-grid">
                  <label className="input-field">
                    <span>Название</span>
                    <input value={newTaskTitle} onChange={(event) => setNewTaskTitle(event.target.value)} />
                  </label>
                  <label className="input-field">
                    <span>Шаблон БД</span>
                    <select
                      value={newTaskTemplateId}
                      onChange={(event) => {
                        const nextTemplateId = event.target.value
                        const nextTemplate = getTemplateById(nextTemplateId, templates)
                        setNewTaskTemplateId(nextTemplateId)
                        setSelectedDatasetIds(nextTemplate?.datasets.map((dataset) => dataset.id) ?? [])
                      }}
                    >
                      {templates.map((template) => (
                        <option key={template.id} value={template.id}>
                          {template.title}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="input-field">
                    <span>Сложность</span>
                    <select value={newTaskDifficulty} onChange={(event) => setNewTaskDifficulty(event.target.value as TaskDefinition['difficulty'])}>
                      <option value="easy">easy</option>
                      <option value="medium">medium</option>
                      <option value="hard">hard</option>
                    </select>
                  </label>
                  <label className="input-field">
                    <span>Тип задачи</span>
                    <input value={newTaskType} onChange={(event) => setNewTaskType(event.target.value)} />
                  </label>
                  <label className="input-field full-span">
                    <span>Описание</span>
                    <textarea value={newTaskDescription} onChange={(event) => setNewTaskDescription(event.target.value)} />
                  </label>
                  <label className="input-field">
                    <span>Конструкции</span>
                    <input value={newTaskConstructs} onChange={(event) => setNewTaskConstructs(event.target.value)} />
                  </label>
                  <label className="input-field full-span">
                    <span>Starter SQL</span>
                    <textarea className="sql-textarea compact-textarea" value={newTaskStarterSql} onChange={(event) => setNewTaskStarterSql(event.target.value)} />
                  </label>
                  <label className="input-field full-span">
                    <span>Эталонный запрос</span>
                    <textarea className="sql-textarea compact-textarea" value={newTaskExpectedQuery} onChange={(event) => setNewTaskExpectedQuery(event.target.value)} />
                  </label>
                  <label className="input-field full-span">
                    <span>Подсказки</span>
                    <textarea value={newTaskHints} onChange={(event) => setNewTaskHints(event.target.value)} />
                  </label>
                </div>

                <div className="section-header">
                  <div>
                    <div className="eyebrow">Datasets</div>
                    <h3>На каких датасетах проверять</h3>
                  </div>
                </div>
                <div className="toggle-grid">
                  {taskTemplate.datasets.map((dataset) => {
                    const isSelected = selectedDatasetIds.includes(dataset.id)
                    return (
                      <button
                        key={dataset.id}
                        className={clsx('toggle-card', isSelected && 'is-on')}
                        onClick={() =>
                          setSelectedDatasetIds((previous) =>
                            previous.includes(dataset.id)
                              ? previous.filter((item) => item !== dataset.id)
                              : [...previous, dataset.id])}
                        type="button"
                      >
                        <span>{dataset.label}</span>
                        <strong>{isSelected ? 'ON' : 'OFF'}</strong>
                      </button>
                    )
                  })}
                </div>

                <div className="dialog-actions">
                  <button className="ghost-button" onClick={() => setTaskDialogOpen(false)} type="button">
                    Отмена
                  </button>
                  <button className="primary-button" onClick={handleCreateTask} type="button">
                    Создать и добавить в семинар
                  </button>
                </div>
              </div>

              <div className="task-dialog-preview">
                <div className="mini-card">
                  <strong>Схема выбранной БД</strong>
                  <p>{taskTemplate.title}</p>
                  <div className="tag-row">
                    <span className="tiny-pill">{taskTemplate.level}</span>
                    <span className="tiny-pill">{taskTemplate.tables.length} таблиц</span>
                    <span className="tiny-pill">{taskTemplate.datasets.length} датасетов</span>
                  </div>
                </div>
                <SchemaPanel template={taskTemplate} mode="schema" />
              </div>
            </div>
          </section>
        </div>
      )}

      {seminarSetupOpen && (
        <div className="dialog-backdrop" onClick={() => setSeminarSetupOpen(false)} role="presentation">
          <section className="dialog-card" onClick={(event) => event.stopPropagation()}>
            <div className="section-header">
              <div>
                <div className="eyebrow">Seminar setup</div>
                <h2>Редактирование текущего семинара</h2>
              </div>
              <button className="ghost-button" onClick={() => setSeminarSetupOpen(false)} type="button">
                Закрыть
              </button>
            </div>

            <div className="form-grid">
              <label className="input-field">
                <span>Название</span>
                <input value={seminar.title} onChange={(event) => onSaveSeminarMeta('title', event.target.value)} />
              </label>
              <label className="input-field">
                <span>Код доступа</span>
                <input value={seminar.accessCode} onChange={(event) => onSaveSeminarMeta('accessCode', event.target.value)} />
              </label>
              <label className="input-field full-span">
                <span>Описание</span>
                <textarea value={seminar.description} onChange={(event) => onSaveSeminarMeta('description', event.target.value)} />
              </label>
              <label className="input-field">
                <span>Начало</span>
                <input value={seminar.startTime} onChange={(event) => onSaveSeminarMeta('startTime', event.target.value)} />
              </label>
              <label className="input-field">
                <span>Окончание</span>
                <input value={seminar.endTime} onChange={(event) => onSaveSeminarMeta('endTime', event.target.value)} />
              </label>
            </div>
          </section>
        </div>
      )}

      {seminarCreateOpen && (
        <div className="dialog-backdrop" onClick={() => setSeminarCreateOpen(false)} role="presentation">
          <section className="dialog-card" onClick={(event) => event.stopPropagation()}>
            <div className="section-header">
              <div>
                <div className="eyebrow">Create next seminar</div>
                <h2>Создать новый семинар</h2>
              </div>
              <button className="ghost-button" onClick={() => setSeminarCreateOpen(false)} type="button">
                Закрыть
              </button>
            </div>

            <div className="form-grid">
              <label className="input-field">
                <span>Название</span>
                <input value={newSeminarTitle} onChange={(event) => setNewSeminarTitle(event.target.value)} />
              </label>
              <label className="input-field">
                <span>Шаблон БД</span>
                <select value={newSeminarTemplateId} onChange={(event) => setNewSeminarTemplateId(event.target.value)}>
                  {templates.map((template) => (
                    <option key={template.id} value={template.id}>
                      {template.title}
                    </option>
                  ))}
                </select>
              </label>
              <label className="input-field full-span">
                <span>Описание</span>
                <textarea value={newSeminarDescription} onChange={(event) => setNewSeminarDescription(event.target.value)} />
              </label>
              <label className="input-field">
                <span>Старт</span>
                <input value={newSeminarStart} onChange={(event) => setNewSeminarStart(event.target.value)} />
              </label>
            </div>

            <div className="dialog-actions">
              <button className="ghost-button" onClick={() => setSeminarCreateOpen(false)} type="button">
                Отмена
              </button>
              <button
                className="primary-button"
                onClick={() => {
                  onCreateSeminar({
                    title: newSeminarTitle,
                    description: newSeminarDescription,
                    templateId: newSeminarTemplateId,
                    startTime: newSeminarStart,
                  })
                  setSeminarCreateOpen(false)
                }}
                type="button"
              >
                Добавить в каталог семинаров
              </button>
            </div>
          </section>
        </div>
      )}

      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Seminar activity</div>
            <h2>Прогресс, рейтинг, уведомления и ревью</h2>
            <p>Вся текущая активность группы собрана в одном блоке.</p>
          </div>
          <div className="filter-row">
            <label className="search-input">
              <span>Студент</span>
              <input
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Поиск студента"
              />
            </label>
            <label className="select-wrap compact">
              <span>Статус</span>
              <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as typeof statusFilter)}>
                <option value="all">Все</option>
                <option value="correct">Решение принято</option>
                <option value="incorrect">Не принято</option>
                <option value="runtime-error">Ошибка SQL</option>
                <option value="blocked">Запрещено</option>
              </select>
            </label>
            <label className="select-wrap compact">
              <span>Задача</span>
              <select value={taskFilter} onChange={(event) => setTaskFilter(event.target.value)}>
                <option value="all">Все</option>
                {seminar.taskIds.map((taskId) => (
                  <option key={taskId} value={taskId}>
                    {getTaskById(taskId, tasks)?.title ?? taskId}
                  </option>
                ))}
              </select>
            </label>
          </div>
        </div>

        <div className="teacher-activity-grid">
          <div className="teacher-activity-main">
            <div className="section-header">
              <div>
                <div className="eyebrow">Progress matrix</div>
                <h3>Кто что сдавал</h3>
              </div>
            </div>

            <div className="matrix-table">
              <div className="matrix-head">
                <span>Студент</span>
                {seminar.taskIds.map((taskId) => (
                  <span key={taskId}>{getTaskById(taskId, tasks)?.title}</span>
                ))}
              </div>
              {filteredStudents.map((student) => (
                <div key={student.id} className="matrix-row">
                  <span>{student.fullName}</span>
                  {seminar.taskIds.map((taskId) => {
                    const latest = pickLatestByTask(runtime.submissions, student.id, taskId, seminar.id)
                    return (
                      <span key={`${student.id}-${taskId}`} className={`badge badge-${queryStatusTone(latest?.status)}`}>
                        {queryStatusLabel(latest?.status ?? 'waiting')}
                      </span>
                    )
                  })}
                </div>
              ))}
            </div>
          </div>

          <div className="teacher-activity-side">
            <div className="mini-card">
              <strong>Топ участников</strong>
              <div className="leaderboard-table">
                {leaderboard.map((entry) => (
                  <div key={entry.user.id} className="leaderboard-item">
                    <div>
                      <strong>#{entry.rank} {entry.user.fullName}</strong>
                      <p>{entry.solvedCount} задач · {entry.attemptsCount} попыток</p>
                    </div>
                    <span>{entry.speedScore ? `${entry.speedScore} ms` : '—'}</span>
                  </div>
                ))}
              </div>
            </div>

            <div className="mini-card">
              <strong>Уведомления</strong>
              <div className="notification-list">
                {[...runtime.notifications]
                  .filter((notification) => notification.seminarId === seminar.id)
                  .sort((left, right) => right.createdAt.localeCompare(left.createdAt))
                  .slice(0, 8)
                  .map((notification) => (
                    <article key={notification.id} className={`notification-card ${notification.level}`}>
                      <strong>{notification.title}</strong>
                      <p>{notification.body}</p>
                      <span>{formatDateTime(notification.createdAt)}</span>
                    </article>
                  ))}
              </div>
            </div>
          </div>
        </div>

        <div className="section-header">
          <div>
            <div className="eyebrow">Проверка решений</div>
            <h3>Решения и тексты запросов</h3>
          </div>
        </div>

        <div className="submission-review-list">
          {filteredSubmissions.map((submission) => (
            <article key={submission.id} className="submission-card">
              <div className="submission-card-top">
                <div>
                  <strong>{getUser(submission.userId)?.fullName}</strong>
                  <p>{getTaskById(submission.taskId, tasks)?.title}</p>
                </div>
                <div className="submission-meta">
                  <span className={`badge badge-${queryStatusTone(submission.status)}`}>{queryStatusLabel(submission.status)}</span>
                  <span>{formatDateTime(submission.submittedAt)}</span>
                </div>
              </div>
              <p className="muted-text">{localizeUiText(submission.validationDetails.summary)}</p>
              <pre className="sql-preview">{submission.sqlText}</pre>
            </article>
          ))}
          {filteredSubmissions.length === 0 && <div className="empty-state">По текущим фильтрам решений нет.</div>}
        </div>
      </section>

      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Банк задач</div>
            <h2>Доступные задачи для добавления</h2>
          </div>
        </div>
        <div className="module-grid">
          {availableTasks.map((task) => (
            <article key={task.id} className="mini-card">
              <strong>{task.title}</strong>
              <p>{task.description}</p>
              <div className="tag-row">
                <span className="tiny-pill">{task.taskType}</span>
                <span className="tiny-pill">{task.difficulty}</span>
              </div>
              <button className="secondary-button" onClick={() => onAssignTask(task.id)}>
                Добавить в семинар
              </button>
            </article>
          ))}
          {availableTasks.length === 0 && <div className="empty-state">Все доступные задачи уже добавлены в текущий семинар.</div>}
        </div>
      </section>

      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Analytics</div>
            <h2>Проблемные задания и среднее время</h2>
            <p>Аналитика вынесена отдельно, а создание и редактирование семинаров открываются из верхнего блока.</p>
          </div>
        </div>
        <div className="event-feed">
          {[...analytics]
            .sort((left, right) => {
              if (right.attempts !== left.attempts) {
                return right.attempts - left.attempts
              }

              return left.correctCount - right.correctCount
            })
            .map((item) => (
              <article key={item.taskId} className="event-item">
                <div>
                  <strong>{item.taskTitle}</strong>
                  <p>{item.correctCount} корректных решений из {item.attempts} попыток</p>
                </div>
                <div>
                  <span>{item.avgTime ? `${item.avgTime} ms` : '—'}</span>
                  <p>{item.firstCorrectAt ? formatDateTime(item.firstCorrectAt) : 'ещё не решали'}</p>
                </div>
              </article>
            ))}
        </div>
      </section>

      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Audit</div>
            <h2>Поток событий семинара</h2>
          </div>
        </div>
        <div className="event-feed">
          {[...runtime.eventLogs]
            .filter((event) => event.seminarId === seminar.id)
            .sort((left, right) => right.createdAt.localeCompare(left.createdAt))
            .slice(0, 12)
            .map((event) => (
              <article key={event.id} className="event-item">
                <div>
                  <strong>{event.eventType}</strong>
                  <p>{getUser(event.userId)?.fullName ?? event.userId}</p>
                </div>
                <span>{formatDateTime(event.createdAt)}</span>
              </article>
            ))}
        </div>
      </section>
    </div>
  )
}

const PlaygroundPage = ({
  currentUser,
  runtime,
  templates,
  playgroundChallenges,
  onSaveDraft,
  onSelectChallenge,
  onSelectPlaygroundTemplate,
  onSelectPlaygroundDataset,
  onRunPlaygroundQuery,
  onValidatePlaygroundChallenge,
}: {
  currentUser: User
  runtime: PlatformRuntime
  templates: DBTemplate[]
  playgroundChallenges: PlaygroundChallenge[]
  onSaveDraft: (key: string, value: string) => void
  onSelectChallenge: (challengeId: string) => void
  onSelectPlaygroundTemplate: (templateId: string) => void
  onSelectPlaygroundDataset: (datasetId: string) => void
  onRunPlaygroundQuery: (challenge: PlaygroundChallenge, sqlText: string) => Promise<void>
  onValidatePlaygroundChallenge: (challenge: PlaygroundChallenge, sqlText: string) => Promise<void>
}) => {
  const template = getTemplateById(runtime.selectedPlaygroundTemplateId, templates) ?? templates[0]
  const allTemplateChallenges = getTemplateChallenges(template, playgroundChallenges)
  const resolvedChallenge = getChallengeById(runtime.selectedPlaygroundChallengeId, templates, playgroundChallenges)
    ?? allTemplateChallenges[0]
  const [activeTab, setActiveTab] = useState<'schema' | 'diagram' | 'examples'>('diagram')
  const [difficultyFilter, setDifficultyFilter] = useState<'all' | PlaygroundChallenge['difficulty']>('all')
  const [topicFilter, setTopicFilter] = useState('all')
  const [constructFilter, setConstructFilter] = useState('all')

  const filteredChallenges = allTemplateChallenges.filter((challenge) => {
    if (difficultyFilter !== 'all' && challenge.difficulty !== difficultyFilter) {
      return false
    }

    if (topicFilter !== 'all' && challenge.topic !== topicFilter) {
      return false
    }

    if (constructFilter !== 'all' && !challenge.constructs.includes(constructFilter)) {
      return false
    }

    return true
  })

  const challenge = filteredChallenges.find((item) => item.id === resolvedChallenge.id)
    ?? filteredChallenges[0]
    ?? resolvedChallenge

  const storageKey = draftKey(currentUser.id, 'playground', challenge.id)
  const initialDraft = runtime.drafts[storageKey] ?? challenge.starterSql
  const latestRun = pickLatestRun(runtime.queryRuns, currentUser.id, 'playground', challenge.id)
  const challengeTopics = [...new Set(allTemplateChallenges.map((item) => item.topic))]
  const challengeConstructs = [...new Set(allTemplateChallenges.flatMap((item) => item.constructs))]
  const validationAvailable = hasChallengeDefinitions(template.id, playgroundChallenges)

  return (
    <div className="page-content">
      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Template</div>
            <h2>{template.title}</h2>
            <p>{template.description}</p>
          </div>
        </div>

        <label className="select-wrap full-width playground-template-select">
          <span>Шаблон БД</span>
          <select
            value={template.id}
            onChange={(event) => onSelectPlaygroundTemplate(event.target.value)}
          >
            {templates.map((item) => (
              <option key={item.id} value={item.id}>
                {item.title}
              </option>
            ))}
          </select>
        </label>

        <div className="tab-row">
          {(['schema', 'diagram', 'examples'] as const).map((tab) => (
            <button
              key={tab}
              className={clsx('tab-button', activeTab === tab && 'is-active')}
              onClick={() => setActiveTab(tab)}
            >
              {tab}
            </button>
          ))}
        </div>

        <SchemaPanel template={template} mode={activeTab} />
      </section>

      <div className="workspace-grid">
      <aside className="panel sidebar-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Playground</div>
            <h2>Свободная практика</h2>
            <p>Можно переключать шаблоны, фильтровать задания и выбирать случайный кейс.</p>
          </div>
        </div>

        <div className="filter-grid">
          <label className="select-wrap compact">
            <span>Сложность</span>
            <select value={difficultyFilter} onChange={(event) => setDifficultyFilter(event.target.value as typeof difficultyFilter)}>
              <option value="all">Все</option>
              <option value="easy">easy</option>
              <option value="medium">medium</option>
              <option value="hard">hard</option>
            </select>
          </label>
          <label className="select-wrap compact">
            <span>Тема</span>
            <select value={topicFilter} onChange={(event) => setTopicFilter(event.target.value)}>
              <option value="all">Все</option>
              {challengeTopics.map((topic) => (
                <option key={topic} value={topic}>{topic}</option>
              ))}
            </select>
          </label>
          <label className="select-wrap compact">
            <span>Конструкция</span>
            <select value={constructFilter} onChange={(event) => setConstructFilter(event.target.value)}>
              <option value="all">Все</option>
              {challengeConstructs.map((construct) => (
                <option key={construct} value={construct}>{construct}</option>
              ))}
            </select>
          </label>
        </div>

        <label className="select-wrap full-width">
          <span>Датасет</span>
          <select
            value={runtime.selectedPlaygroundDatasetId}
            onChange={(event) => onSelectPlaygroundDataset(event.target.value)}
          >
            {template.datasets.map((dataset) => (
              <option key={dataset.id} value={dataset.id}>
                {dataset.label}
              </option>
            ))}
          </select>
        </label>

        <button
          className="ghost-button"
          onClick={() => {
            const pool = filteredChallenges.length > 0 ? filteredChallenges : allTemplateChallenges
            const random = pool[Math.floor(Math.random() * pool.length)]
            onSelectChallenge(random.id)
          }}
        >
          Случайное задание
        </button>

        <div className="task-list">
          {filteredChallenges.map((item) => (
            <button
              key={item.id}
              className={clsx('task-card', item.id === challenge.id && 'is-active')}
              onClick={() => onSelectChallenge(item.id)}
            >
              <div className="task-card-top">
                <span>{item.topic}</span>
                <span className="tiny-pill">{item.feedbackMode}</span>
              </div>
              <strong>{item.title}</strong>
              <p>{item.description}</p>
              <div className="tag-row">
                {item.constructs.map((construct) => (
                  <span key={construct} className="tiny-pill">
                    {construct}
                  </span>
                ))}
              </div>
            </button>
          ))}
          {filteredChallenges.length === 0 && <div className="empty-state">По выбранным фильтрам задач нет.</div>}
        </div>
      </aside>

      <div className="workspace-main">
        <section className="panel task-brief">
          <div className="section-header">
            <div>
              <div className="eyebrow">Challenge</div>
              <h2>{challenge.title}</h2>
              <p>{challenge.description}</p>
            </div>
            <div className="task-meta">
              <span className="tiny-pill">{challenge.difficulty}</span>
              <span className="tiny-pill">{challenge.feedbackMode}</span>
              {!hasChallengeDefinitions(template.id, playgroundChallenges) && <span className="tiny-pill">freeform</span>}
            </div>
          </div>
        </section>

        <DraftSqlEditor
          key={storageKey}
          storageKey={storageKey}
          initialSql={initialDraft}
          title="Редактор запросов"
          subtitle="Запуск без ограничений по попыткам. Для импортированных шаблонов можно использовать режим свободного исследования."
          onSaveDraft={onSaveDraft}
          onExecute={(sqlText) => void onRunPlaygroundQuery(challenge, sqlText)}
          onSubmit={validationAvailable ? (sqlText) => void onValidatePlaygroundChallenge(challenge, sqlText) : undefined}
        />

        <section className="panel">
          <div className="result-banner">
            <div>
              <div className="eyebrow">Результат запуска</div>
              <h2>Последний результат</h2>
            </div>
          </div>

          {latestRun?.errorMessage && <div className="alert danger">{latestRun.errorMessage}</div>}

          {latestRun?.status === 'success' && challenge.feedbackMode !== 'full' && (
            <div className="alert neutral">
              {challenge.feedbackMode === 'preview' && 'Режим предварительного просмотра: показываем таблицу и сообщение о совпадении.'}
              {challenge.feedbackMode === 'row-count' && `Режим подсчёта строк: в текущем результате ${latestRun.rowCount} строк.`}
              {challenge.feedbackMode === 'match-only' && 'Режим только проверки совпадения: ориентируйтесь на статус проверки.'}
            </div>
          )}

          <ResultGrid result={challenge.feedbackMode === 'match-only' ? undefined : latestRun?.result} />
        </section>
      </div>
      </div>
    </div>
  )
}

const AdminPage = ({
  currentUser,
  runtime,
  users,
  groups,
  templates,
  onCreateGroup,
  onUpdateGroup,
  onDeleteGroup,
  onCreateUser,
  onUpdateUser,
  onDeleteUser,
  onImportTemplate,
}: {
  currentUser: User
  runtime: PlatformRuntime
  users: User[]
  groups: Group[]
  templates: DBTemplate[]
  onCreateGroup: (payload: { title: string; stream: string }) => Promise<void>
  onUpdateGroup: (payload: { groupId: string; title: string; stream: string }) => Promise<void>
  onDeleteGroup: (groupId: string) => Promise<void>
  onCreateUser: (payload: {
    role: Role
    fullName: string
    login: string
    groupId?: string
    password?: string
  }) => Promise<void>
  onUpdateUser: (payload: {
    userId: string
    fullName: string
    login: string
    groupId?: string
    password?: string
  }) => Promise<void>
  onDeleteUser: (userId: string) => Promise<void>
  onImportTemplate: (payload: {
    title: string
    description: string
    schemaSql: string
    datasets: Array<{
      label: string
      description: string
      seedSql: string
    }>
    level: DBTemplate['level']
  }) => Promise<void>
}) => {
  const [adminSection, setAdminSection] = useState<'users' | 'templates' | 'import' | 'telemetry'>('users')
  const [groupTitle, setGroupTitle] = useState('')
  const [groupStream, setGroupStream] = useState('')
  const [editingGroupId, setEditingGroupId] = useState('')
  const [editingGroupTitle, setEditingGroupTitle] = useState('')
  const [editingGroupStream, setEditingGroupStream] = useState('')
  const [studentName, setStudentName] = useState('')
  const [studentLogin, setStudentLogin] = useState('')
  const [studentGroupId, setStudentGroupId] = useState('')
  const [teacherName, setTeacherName] = useState('')
  const [teacherLogin, setTeacherLogin] = useState('')
  const [teacherPassword, setTeacherPassword] = useState('')
  const [editingUserId, setEditingUserId] = useState('')
  const [editingUserName, setEditingUserName] = useState('')
  const [editingUserLogin, setEditingUserLogin] = useState('')
  const [editingUserGroupId, setEditingUserGroupId] = useState('')
  const [editingUserPassword, setEditingUserPassword] = useState('')
  const [memberBusy, setMemberBusy] = useState(false)
  const [title, setTitle] = useState('Imported Training Schema')
  const [description, setDescription] = useState('Шаблон, загруженный из SQL-скрипта через admin-панель.')
  const [schemaSql, setSchemaSql] = useState('CREATE TABLE seminar_users (id INTEGER PRIMARY KEY, full_name TEXT NOT NULL);')
  const [level, setLevel] = useState<DBTemplate['level']>('easy')
  const [datasets, setDatasets] = useState([
    {
      label: 'Dataset A',
      description: 'Основной датасет',
      seedSql: "INSERT INTO seminar_users(id, full_name) VALUES (1, 'Alice');",
    },
    {
      label: 'Dataset B',
      description: 'Скрытый датасет',
      seedSql: "INSERT INTO seminar_users(id, full_name) VALUES (2, 'Bob');",
    },
  ])
  const [busy, setBusy] = useState(false)

  if (currentUser.role !== 'admin') {
    return <div className="panel restricted-panel">Панель администратора доступна только роли `admin`.</div>
  }

  const eventsByType = runtime.eventLogs.reduce<Record<string, number>>((accumulator, event) => {
    accumulator[event.eventType] = (accumulator[event.eventType] ?? 0) + 1
    return accumulator
  }, {})
  const students = users.filter((user) => user.role === 'student')
  const teachers = users.filter((user) => user.role !== 'student')

  const startEditingGroup = (group: Group) => {
    setEditingGroupId(group.id)
    setEditingGroupTitle(group.title)
    setEditingGroupStream(group.stream)
  }

  const startEditingUser = (user: User) => {
    setEditingUserId(user.id)
    setEditingUserName(user.fullName)
    setEditingUserLogin(user.login)
    setEditingUserGroupId(user.groupId ?? '')
    setEditingUserPassword('')
  }

  return (
    <div className="page-content">
      <section className="panel full-width-panel">
        <div className="section-header">
          <div>
            <div className="eyebrow">Admin</div>
            <h2>Администрирование платформы</h2>
            <p>Управление пользователями, библиотекой шаблонов, импортом схем и телеметрией.</p>
          </div>
        </div>

        <div className="tab-row">
          {([
            ['users', 'Группы и студенты'],
            ['templates', 'Шаблоны'],
            ['import', 'Импорт схемы'],
            ['telemetry', 'Телеметрия'],
          ] as const).map(([key, label]) => (
            <button
              key={key}
              className={clsx('tab-button', adminSection === key && 'is-active')}
              onClick={() => setAdminSection(key)}
              type="button"
            >
              {label}
            </button>
          ))}
        </div>
      </section>

      {adminSection === 'users' && (
        <section className="panel full-width-panel">
          <div className="section-header">
            <div>
              <div className="eyebrow">Users</div>
              <h2>Группы и студенты</h2>
              <p>Добавление, редактирование и удаление групп, студентов и преподавателей.</p>
            </div>
          </div>

          <div className="teacher-control-nest">
            <div className="section-header">
              <div>
                <div className="eyebrow">Groups</div>
                <h3>Группы</h3>
              </div>
            </div>
            <div className="form-grid">
              <label className="input-field">
                <span>Название группы</span>
                <input value={groupTitle} onChange={(event) => setGroupTitle(event.target.value)} />
              </label>
              <label className="input-field">
                <span>Направление</span>
                <input value={groupStream} onChange={(event) => setGroupStream(event.target.value)} />
              </label>
            </div>
            <div className="control-row">
              <button
                className="primary-button"
                disabled={memberBusy}
                onClick={() => {
                  setMemberBusy(true)
                  void onCreateGroup({ title: groupTitle, stream: groupStream })
                    .then(() => {
                      setGroupTitle('')
                      setGroupStream('')
                    })
                    .finally(() => setMemberBusy(false))
                }}
                type="button"
              >
                Добавить группу
              </button>
            </div>

            <div className="user-grid">
              {groups.map((group) => {
                const studentsInGroup = students.filter((user) => user.groupId === group.id).length
                const isEditing = editingGroupId === group.id
                return (
                  <article key={group.id} className="mini-card">
                    {isEditing ? (
                      <>
                        <label className="input-field">
                          <span>Название</span>
                          <input value={editingGroupTitle} onChange={(event) => setEditingGroupTitle(event.target.value)} />
                        </label>
                        <label className="input-field">
                          <span>Направление</span>
                          <input value={editingGroupStream} onChange={(event) => setEditingGroupStream(event.target.value)} />
                        </label>
                        <div className="control-row">
                          <button
                            className="primary-button"
                            disabled={memberBusy}
                            onClick={() => {
                              setMemberBusy(true)
                              void onUpdateGroup({
                                groupId: group.id,
                                title: editingGroupTitle,
                                stream: editingGroupStream,
                              })
                                .then(() => setEditingGroupId(''))
                                .finally(() => setMemberBusy(false))
                            }}
                            type="button"
                          >
                            Сохранить
                          </button>
                          <button className="ghost-button" onClick={() => setEditingGroupId('')} type="button">
                            Отмена
                          </button>
                        </div>
                      </>
                    ) : (
                      <>
                        <strong>{group.title}</strong>
                        <p>{group.stream}</p>
                        <div className="tag-row">
                          <span className="tiny-pill">{studentsInGroup} студентов</span>
                        </div>
                        <div className="control-row">
                          <button className="ghost-button" onClick={() => startEditingGroup(group)} type="button">
                            Редактировать
                          </button>
                          <button
                            className="ghost-button"
                            disabled={memberBusy}
                            onClick={() => {
                              setMemberBusy(true)
                              void onDeleteGroup(group.id).finally(() => setMemberBusy(false))
                            }}
                            type="button"
                          >
                            Удалить
                          </button>
                        </div>
                      </>
                    )}
                  </article>
                )
              })}
            </div>
          </div>

          <div className="teacher-control-nest">
            <div className="section-header">
              <div>
                <div className="eyebrow">Students</div>
                <h3>Студенты</h3>
              </div>
            </div>
            <div className="form-grid">
              <label className="input-field">
                <span>ФИО</span>
                <input value={studentName} onChange={(event) => setStudentName(event.target.value)} />
              </label>
              <label className="input-field">
                <span>Логин</span>
                <input value={studentLogin} onChange={(event) => setStudentLogin(event.target.value)} />
              </label>
              <label className="input-field">
                <span>Группа</span>
                <select value={studentGroupId} onChange={(event) => setStudentGroupId(event.target.value)}>
                  <option value="">Выберите группу</option>
                  {groups.map((group) => (
                    <option key={group.id} value={group.id}>
                      {group.title}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <div className="control-row">
              <button
                className="primary-button"
                disabled={memberBusy}
                onClick={() => {
                  setMemberBusy(true)
                  void onCreateUser({
                    role: 'student',
                    fullName: studentName,
                    login: studentLogin,
                    groupId: studentGroupId,
                  })
                    .then(() => {
                      setStudentName('')
                      setStudentLogin('')
                      setStudentGroupId('')
                    })
                    .finally(() => setMemberBusy(false))
                }}
                type="button"
              >
                Добавить студента
              </button>
            </div>

            <div className="user-grid">
              {students.map((user) => {
                const isEditing = editingUserId === user.id
                return (
                  <article key={user.id} className="mini-card">
                    {isEditing ? (
                      <>
                        <label className="input-field">
                          <span>ФИО</span>
                          <input value={editingUserName} onChange={(event) => setEditingUserName(event.target.value)} />
                        </label>
                        <label className="input-field">
                          <span>Логин</span>
                          <input value={editingUserLogin} onChange={(event) => setEditingUserLogin(event.target.value)} />
                        </label>
                        <label className="input-field">
                          <span>Группа</span>
                          <select value={editingUserGroupId} onChange={(event) => setEditingUserGroupId(event.target.value)}>
                            <option value="">Выберите группу</option>
                            {groups.map((group) => (
                              <option key={group.id} value={group.id}>
                                {group.title}
                              </option>
                            ))}
                          </select>
                        </label>
                        <div className="control-row">
                          <button
                            className="primary-button"
                            disabled={memberBusy}
                            onClick={() => {
                              setMemberBusy(true)
                              void onUpdateUser({
                                userId: user.id,
                                fullName: editingUserName,
                                login: editingUserLogin,
                                groupId: editingUserGroupId,
                              })
                                .then(() => setEditingUserId(''))
                                .finally(() => setMemberBusy(false))
                            }}
                            type="button"
                          >
                            Сохранить
                          </button>
                          <button className="ghost-button" onClick={() => setEditingUserId('')} type="button">
                            Отмена
                          </button>
                        </div>
                      </>
                    ) : (
                      <>
                        <strong>{user.fullName}</strong>
                        <p>{user.login}</p>
                        <div className="tag-row">
                          <span className="tiny-pill">{getGroup(user.groupId ?? '')?.title ?? 'Без группы'}</span>
                        </div>
                        <div className="control-row">
                          <button className="ghost-button" onClick={() => startEditingUser(user)} type="button">
                            Редактировать
                          </button>
                          <button
                            className="ghost-button"
                            disabled={memberBusy}
                            onClick={() => {
                              setMemberBusy(true)
                              void onDeleteUser(user.id).finally(() => setMemberBusy(false))
                            }}
                            type="button"
                          >
                            Удалить
                          </button>
                        </div>
                      </>
                    )}
                  </article>
                )
              })}
            </div>
          </div>

          <div className="teacher-control-nest">
            <div className="section-header">
              <div>
                <div className="eyebrow">Teachers</div>
                <h3>Преподаватели</h3>
              </div>
            </div>
            <div className="form-grid">
              <label className="input-field">
                <span>ФИО</span>
                <input value={teacherName} onChange={(event) => setTeacherName(event.target.value)} />
              </label>
              <label className="input-field">
                <span>Логин</span>
                <input value={teacherLogin} onChange={(event) => setTeacherLogin(event.target.value)} />
              </label>
              <label className="input-field">
                <span>Пароль</span>
                <input type="password" value={teacherPassword} onChange={(event) => setTeacherPassword(event.target.value)} />
              </label>
            </div>
            <div className="control-row">
              <button
                className="primary-button"
                disabled={memberBusy}
                onClick={() => {
                  setMemberBusy(true)
                  void onCreateUser({
                    role: 'teacher',
                    fullName: teacherName,
                    login: teacherLogin,
                    password: teacherPassword,
                  })
                    .then(() => {
                      setTeacherName('')
                      setTeacherLogin('')
                      setTeacherPassword('')
                    })
                    .finally(() => setMemberBusy(false))
                }}
                type="button"
              >
                Добавить преподавателя
              </button>
            </div>

            <div className="user-grid">
              {teachers.map((user) => {
                const isEditing = editingUserId === user.id
                return (
                  <article key={user.id} className="mini-card">
                    {isEditing ? (
                      <>
                        <label className="input-field">
                          <span>ФИО</span>
                          <input value={editingUserName} onChange={(event) => setEditingUserName(event.target.value)} />
                        </label>
                        <label className="input-field">
                          <span>Логин</span>
                          <input value={editingUserLogin} onChange={(event) => setEditingUserLogin(event.target.value)} />
                        </label>
                        <label className="input-field">
                          <span>Новый пароль</span>
                          <input type="password" value={editingUserPassword} onChange={(event) => setEditingUserPassword(event.target.value)} />
                        </label>
                        <div className="tag-row">
                          <span className="tiny-pill">{roleLabel[user.role]}</span>
                        </div>
                        <div className="control-row">
                          <button
                            className="primary-button"
                            disabled={memberBusy}
                            onClick={() => {
                              setMemberBusy(true)
                              void onUpdateUser({
                                userId: user.id,
                                fullName: editingUserName,
                                login: editingUserLogin,
                                password: editingUserPassword,
                              })
                                .then(() => setEditingUserId(''))
                                .finally(() => setMemberBusy(false))
                            }}
                            type="button"
                          >
                            Сохранить
                          </button>
                          <button className="ghost-button" onClick={() => setEditingUserId('')} type="button">
                            Отмена
                          </button>
                        </div>
                      </>
                    ) : (
                      <>
                        <strong>{user.fullName}</strong>
                        <p>{user.login}</p>
                        <div className="tag-row">
                          <span className="tiny-pill">{roleLabel[user.role]}</span>
                        </div>
                        <div className="control-row">
                          <button className="ghost-button" onClick={() => startEditingUser(user)} type="button">
                            Редактировать
                          </button>
                          <button
                            className="ghost-button"
                            disabled={memberBusy}
                            onClick={() => {
                              setMemberBusy(true)
                              void onDeleteUser(user.id).finally(() => setMemberBusy(false))
                            }}
                            type="button"
                          >
                            Удалить
                          </button>
                        </div>
                      </>
                    )}
                  </article>
                )
              })}
            </div>
          </div>
        </section>
      )}

      {adminSection === 'templates' && (
        <section className="panel full-width-panel">
          <div className="section-header">
            <div>
              <div className="eyebrow">Templates</div>
              <h2>Библиотека БД</h2>
              <p>Каталог шаблонов, которые используются в семинарах и playground.</p>
            </div>
          </div>
          <div className="module-grid">
            {templates.map((template) => (
              <article key={template.id} className="mini-card">
                <strong>{template.title}</strong>
                <p>{template.description}</p>
                <div className="tag-row">
                  {template.topics.map((topic) => (
                    <span key={topic} className="tiny-pill">
                      {topic}
                    </span>
                  ))}
                </div>
              </article>
            ))}
          </div>
        </section>
      )}

      {adminSection === 'import' && (
        <section className="panel full-width-panel">
          <div className="section-header">
            <div>
              <div className="eyebrow">Import schema</div>
              <h2>Импорт схемы и настройка датасетов</h2>
              <p>Скрипт выполняется локально в изолированной БД, после чего таблицы автоматически попадают в каталог шаблонов.</p>
            </div>
          </div>

          <div className="form-grid">
            <label className="input-field">
              <span>Название шаблона</span>
              <input value={title} onChange={(event) => setTitle(event.target.value)} />
            </label>
            <label className="input-field">
              <span>Уровень</span>
              <select value={level} onChange={(event) => setLevel(event.target.value as DBTemplate['level'])}>
                <option value="easy">easy</option>
                <option value="medium">medium</option>
                <option value="hard">hard</option>
              </select>
            </label>
            <label className="input-field full-span">
              <span>Описание</span>
              <textarea value={description} onChange={(event) => setDescription(event.target.value)} />
            </label>
            <label className="input-field full-span">
              <span>SQL-схема</span>
              <textarea className="sql-textarea" value={schemaSql} onChange={(event) => setSchemaSql(event.target.value)} />
            </label>
          </div>

          <div className="dataset-editor-list">
            {datasets.map((dataset, index) => (
              <article key={`${dataset.label}-${index}`} className="dataset-editor-card">
                <div className="section-header">
                  <div>
                    <div className="eyebrow">Dataset {index + 1}</div>
                    <h3>{dataset.label}</h3>
                  </div>
                  {datasets.length > 1 && (
                    <button
                      className="ghost-button"
                      onClick={() =>
                        setDatasets((previous) => previous.filter((_, datasetIndex) => datasetIndex !== index))}
                    >
                      Удалить
                    </button>
                  )}
                </div>
                <div className="form-grid">
                  <label className="input-field">
                    <span>Название</span>
                    <input
                      value={dataset.label}
                      onChange={(event) =>
                        setDatasets((previous) =>
                          previous.map((item, datasetIndex) =>
                            datasetIndex === index ? { ...item, label: event.target.value } : item))}
                    />
                  </label>
                  <label className="input-field">
                    <span>Описание</span>
                    <input
                      value={dataset.description}
                      onChange={(event) =>
                        setDatasets((previous) =>
                          previous.map((item, datasetIndex) =>
                            datasetIndex === index ? { ...item, description: event.target.value } : item))}
                    />
                  </label>
                  <label className="input-field full-span">
                    <span>Seed SQL</span>
                    <textarea
                      className="sql-textarea compact-textarea"
                      value={dataset.seedSql}
                      onChange={(event) =>
                        setDatasets((previous) =>
                          previous.map((item, datasetIndex) =>
                            datasetIndex === index ? { ...item, seedSql: event.target.value } : item))}
                    />
                  </label>
                </div>
              </article>
            ))}
          </div>

          <button
            className="ghost-button"
            onClick={() =>
              setDatasets((previous) => [
                ...previous,
                {
                  label: `Dataset ${String.fromCharCode(65 + previous.length)}`,
                  description: 'Дополнительный датасет',
                  seedSql: '',
                },
              ])}
          >
            Добавить датасет
          </button>

          <button
            className="primary-button"
            disabled={busy}
            onClick={() => {
              setBusy(true)
              void onImportTemplate({
                title,
                description,
                schemaSql,
                datasets,
                level,
              }).finally(() => setBusy(false))
            }}
          >
            {busy ? 'Импортируем...' : 'Импортировать шаблон'}
          </button>
        </section>
      )}

      {adminSection === 'telemetry' && (
        <section className="panel full-width-panel">
          <div className="section-header">
            <div>
              <div className="eyebrow">Telemetry</div>
              <h2>Телеметрия</h2>
              <p>Технические события и частота их возникновения.</p>
            </div>
          </div>
          <div className="event-feed">
            {Object.entries(eventsByType).map(([eventType, count]) => (
              <article key={eventType} className="event-item">
                <div>
                  <strong>{eventType}</strong>
                  <p>Событий типа</p>
                </div>
                <div>
                  <span>{count}</span>
                </div>
              </article>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

function App() {
  const [runtime, setRuntime] = useState<PlatformRuntime>(emptyRuntime)
  const [catalog, setCatalog] = useState<AppCatalog>(emptyCatalog)
  const [engineReady, setEngineReady] = useState(false)
  const [loginError, setLoginError] = useState<string | null>(null)
  const [loginPending, setLoginPending] = useState(false)
  const [appError, setAppError] = useState<string | null>(null)
  const [authChecking, setAuthChecking] = useState(true)
  const [token, setToken] = useState<string | null>(() => getStoredToken())

  useEffect(() => {
    void api.health()
      .then(() => setEngineReady(true))
      .catch(() => setEngineReady(false))
  }, [])

  useEffect(() => {
    if (!token) {
      setAuthChecking(false)
      return
    }

    let cancelled = false
    void api.bootstrap(token)
      .then(({ runtime: nextRuntime, catalog: nextCatalog }) => {
        if (cancelled) {
          return
        }
        setCatalog(nextCatalog)
        setRuntime(nextRuntime)
        setAppError(null)
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return
        }
        clearStoredToken()
        setToken(null)
        setCatalog(emptyCatalog())
        setRuntime(emptyRuntime())
        setLoginError(error instanceof Error ? error.message : 'Не удалось восстановить сессию.')
      })
      .finally(() => {
        if (!cancelled) {
          setAuthChecking(false)
        }
      })

    return () => {
      cancelled = true
    }
  }, [token])

  useEffect(() => {
    if (!token || !runtime.isAuthenticated) {
      return
    }

    const socket: RuntimeSocket = api.connect(token, (nextRuntime, nextCatalog) => {
      setCatalog(nextCatalog)
      setRuntime(nextRuntime)
      setAppError(null)
      setEngineReady(true)
    })

    socket.addEventListener('error', () => {
      setAppError('WebSocket соединение с backend недоступно.')
    })

    return () => {
      socket.close()
    }
  }, [token, runtime.isAuthenticated])

  const groups = useMemo(() => catalog.groups, [catalog.groups])
  const users = useMemo(() => catalog.users, [catalog.users])
  appGroups = groups
  appUsers = users

  const currentUser = getUser(runtime.currentUserId) ?? users[0] ?? anonymousUser
  const templates = useMemo(() => catalog.templates, [catalog.templates])
  const seminars = useMemo(
    () => catalog.seminars.map((item) => resolveSeminar(item, runtime)),
    [catalog.seminars, runtime],
  )
  const accessibleSeminars = useMemo(
    () =>
      currentUser.role === 'student'
        ? seminars.filter((item) => item.status === 'live' && item.studentIds.includes(currentUser.id))
        : seminars,
    [currentUser.id, currentUser.role, seminars],
  )
  const currentSeminar = useMemo(() => {
    const selectedSeminarId = runtime.selectedSeminarByUser[currentUser.id]
    return accessibleSeminars.find((item) => item.id === selectedSeminarId)
      ?? seminars.find((item) => item.id === selectedSeminarId)
      ?? accessibleSeminars[0]
      ?? seminars[0]
      ?? resolveSeminar(emptySeminar, runtime)
  }, [accessibleSeminars, currentUser.id, runtime, seminars])
  const tasks = useMemo(() => {
    const catalogTasks = getTaskCatalog(catalog.tasks)
    if (currentUser.role !== 'student' || currentSeminar.settings.referenceSolutionVisible) {
      return catalogTasks
    }

    return catalogTasks.map((task) => ({
      ...task,
      starterSql: '',
      expectedQuery: '',
    }))
  }, [catalog.tasks, currentSeminar.settings.referenceSolutionVisible, currentUser.role])

  const performAction = async (action: string, payload: Record<string, unknown> = {}) => {
    if (!token) {
      throw new Error('Сессия не найдена. Войдите снова.')
    }

    const { runtime: nextRuntime, catalog: nextCatalog } = await api.action(token, action, payload)
    setCatalog(nextCatalog)
    setRuntime(nextRuntime)
    setAppError(null)
    return nextRuntime
  }

  const saveDraftValue = (key: string, value: string) => {
    startTransition(() => {
      setRuntime((previous) => ({
        ...previous,
        drafts: {
          ...previous.drafts,
          [key]: value,
        },
      }))
    })

    void performAction('save-draft', { key, value }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось сохранить черновик.')
    })
  }

  const loginTeacher = async (loginValue: string, passwordValue: string) => {
    setLoginPending(true)
    setLoginError(null)

    try {
      const response = await api.loginTeacher(loginValue.trim(), passwordValue)
      setStoredToken(response.token)
      setToken(response.token)
      setCatalog(response.catalog)
      setRuntime(response.runtime)
      setAppError(null)
      setEngineReady(true)
    } catch (error) {
      setLoginError(error instanceof Error ? error.message : 'Не удалось выполнить вход.')
    } finally {
      setLoginPending(false)
    }
  }

  const loginStudent = async (surname: string) => {
    setLoginPending(true)
    setLoginError(null)

    try {
      const response = await api.loginStudent(surname.trim())
      setStoredToken(response.token)
      setToken(response.token)
      setCatalog(response.catalog)
      setRuntime(response.runtime)
      setAppError(null)
      setEngineReady(true)
    } catch (error) {
      setLoginError(error instanceof Error ? error.message : 'Не удалось выполнить вход по фамилии.')
    } finally {
      setLoginPending(false)
    }
  }

  const logout = () => {
    clearStoredToken()
    setToken(null)
    setAppError(null)
    setCatalog(emptyCatalog())
    setRuntime(emptyRuntime())
  }

  const runSeminarQuery = async (task: TaskDefinition, sqlText: string) => {
    try {
      await performAction('run-seminar-query', { taskId: task.id, sqlText })
    } catch (error) {
      setAppError(error instanceof Error ? error.message : 'Не удалось выполнить запрос.')
    }
  }

  const submitSeminarQuery = async (task: TaskDefinition, sqlText: string) => {
    try {
      await performAction('submit-seminar-query', { taskId: task.id, sqlText })
    } catch (error) {
      setAppError(error instanceof Error ? error.message : 'Не удалось отправить решение.')
    }
  }

  const updateSeminarSetting = (setting: keyof Seminar['settings']) => {
    void performAction('toggle-setting', { setting }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось обновить настройки семинара.')
    })
  }

  const updateSeminarMeta = (field: keyof SeminarMetaOverride, value: string) => {
    void performAction('update-seminar-meta', { field, value }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось обновить метаданные семинара.')
    })
  }

  const createSeminar = ({
    title,
    description,
    templateId,
    startTime,
  }: {
    title: string
    description: string
    templateId: string
    startTime: string
  }) => {
    void performAction('create-seminar', { title, description, templateId, startTime }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось создать семинар.')
    })
  }

  const toggleSeminarStatus = () => {
    void performAction('toggle-seminar-status').catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось переключить статус семинара.')
    })
  }

  const setSeminarStatus = (seminarId: string, targetStatus: Seminar['status']) => {
    void (async () => {
      if (runtime.selectedSeminarByUser[currentUser.id] !== seminarId) {
        await performAction('select-seminar', { seminarId })
      }

      const targetSeminar = seminars.find((item) => item.id === seminarId)
      if (targetSeminar && targetSeminar.status !== targetStatus) {
        await performAction('toggle-seminar-status')
      }
    })().catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось изменить статус выбранного семинара.')
    })
  }

  const pickStudent = () => {
    void performAction('pick-student').catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось выбрать студента.')
    })
  }

  const selectSeminar = (seminarId: string) => {
    void performAction('select-seminar', { seminarId }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось переключить семинар.')
    })
  }

  const selectTask = (taskId: string) => {
    startTransition(() => {
      setRuntime((previous) => ({
        ...previous,
        selectedTaskByUser: {
          ...previous.selectedTaskByUser,
          [currentUser.id]: taskId,
        },
      }))
    })

    void performAction('select-task', { taskId }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось открыть задачу.')
    })
  }

  const selectPlaygroundTemplate = (templateId: string) => {
    void performAction('select-playground-template', { templateId }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось переключить playground-шаблон.')
    })
  }

  const selectChallenge = (challengeId: string) => {
    void performAction('select-challenge', { challengeId }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось открыть playground-задачу.')
    })
  }

  const selectPlaygroundDataset = (datasetId: string) => {
    void performAction('select-playground-dataset', { datasetId }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось переключить playground-датасет.')
    })
  }

  const runPlaygroundQuery = async (challenge: PlaygroundChallenge, sqlText: string) => {
    try {
      await performAction('run-playground-query', { challengeId: challenge.id, sqlText })
    } catch (error) {
      setAppError(error instanceof Error ? error.message : 'Не удалось выполнить playground-запрос.')
    }
  }

  const validatePlaygroundChallenge = async (challenge: PlaygroundChallenge, sqlText: string) => {
    try {
      await performAction('validate-playground-challenge', { challengeId: challenge.id, sqlText })
    } catch (error) {
      setAppError(error instanceof Error ? error.message : 'Не удалось проверить playground-задачу.')
    }
  }

  const importTemplate = async ({
    title,
    description,
    schemaSql,
    datasets,
    level,
  }: {
    title: string
    description: string
    schemaSql: string
    datasets: Array<{
      label: string
      description: string
      seedSql: string
    }>
    level: DBTemplate['level']
  }) => {
    try {
      await performAction('import-template', { title, description, schemaSql, datasets, level })
    } catch (error) {
      setAppError(error instanceof Error ? error.message : 'Не удалось импортировать шаблон.')
      throw error
    }
  }

  const createGroup = async (payload: { title: string; stream: string }) => {
    await performAction('create-group', payload)
  }

  const updateGroup = async (payload: { groupId: string; title: string; stream: string }) => {
    await performAction('update-group', payload)
  }

  const deleteGroup = async (groupId: string) => {
    await performAction('delete-group', { groupId })
  }

  const createUserRecord = async (payload: {
    role: Role
    fullName: string
    login: string
    groupId?: string
    password?: string
  }) => {
    await performAction('create-user', payload)
  }

  const updateUserRecord = async (payload: {
    userId: string
    fullName: string
    login: string
    groupId?: string
    password?: string
  }) => {
    await performAction('update-user', payload)
  }

  const deleteUserRecord = async (userId: string) => {
    await performAction('delete-user', { userId })
  }

  const createTask = (payload: {
    title: string
    description: string
    templateId: string
    difficulty: TaskDefinition['difficulty']
    taskType: string
    constructsText: string
    starterSql: string
    expectedQuery: string
    hintsText: string
    datasetIds: string[]
  }) => {
    void performAction('create-task', payload).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось создать задачу.')
    })
  }

  const assignTask = (taskId: string) => {
    void performAction('assign-task', { taskId }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось добавить задачу в семинар.')
    })
  }

  const removeTask = (taskId: string) => {
    void performAction('remove-task', { taskId }).catch((error: unknown) => {
      setAppError(error instanceof Error ? error.message : 'Не удалось удалить задачу из семинара.')
    })
  }

  const resetPlatformState = () => {
    if (!token) {
      return
    }

    void api.reset(token)
      .then(({ runtime: nextRuntime, catalog: nextCatalog }) => {
        setCatalog(nextCatalog)
        setRuntime(nextRuntime)
        setLoginError(null)
        setAppError(null)
      })
      .catch((error: unknown) => {
        setAppError(error instanceof Error ? error.message : 'Не удалось сбросить данные.')
      })
  }

  if (authChecking) {
    return (
      <div className="login-page">
        <section className="auth-card">
          <div className="eyebrow">Игровая площадка ИВТ</div>
          <h1>Подключение к backend</h1>
          <p>Восстанавливаем сессию и синхронизируем состояние семинара.</p>
        </section>
      </div>
    )
  }

  if (!runtime.isAuthenticated) {
    return (
      <LoginScreen
        onTeacherLogin={loginTeacher}
        onStudentLogin={loginStudent}
        onResetError={() => setLoginError(null)}
        loading={loginPending}
        error={loginError}
      />
    )
  }

  return (
    <HashRouter>
      <Shell
        currentUser={currentUser}
        runtime={runtime}
        seminar={currentSeminar}
        onLogout={logout}
        engineReady={engineReady}
        serverError={appError}
      >
        <Routes>
          <Route path="/" element={<Navigate to={currentUser.role === 'student' ? '/seminar' : '/teacher'} replace />} />
          <Route
            path="/overview"
            element={currentUser.role === 'student'
              ? <Navigate to="/seminar" replace />
              : <OverviewPage runtime={runtime} seminar={currentSeminar} seminars={seminars} onSelectSeminar={selectSeminar} />}
          />
          <Route
            path="/seminar"
            element={(
              <StudentWorkspace
                currentUser={currentUser}
                runtime={runtime}
                seminar={currentSeminar}
                seminars={accessibleSeminars}
                tasks={tasks}
                templates={templates}
                onSelectSeminar={selectSeminar}
                onSelectTask={selectTask}
                onSaveDraft={saveDraftValue}
                onRunSeminarQuery={runSeminarQuery}
                onSubmitSeminarQuery={submitSeminarQuery}
              />
            )}
          />
          <Route
            path="/teacher"
            element={currentUser.role === 'student'
              ? <Navigate to="/seminar" replace />
              : (
                <TeacherDashboard
                  currentUser={currentUser}
                  runtime={runtime}
                  seminar={currentSeminar}
                  seminars={seminars}
                  tasks={tasks}
                  templates={templates}
                  onToggleSetting={updateSeminarSetting}
                  onToggleSeminarStatus={toggleSeminarStatus}
                  onSetSeminarStatus={setSeminarStatus}
                  onSelectSeminar={selectSeminar}
                  onPickStudent={pickStudent}
                  onSaveSeminarMeta={updateSeminarMeta}
                  onCreateSeminar={createSeminar}
                  onCreateTask={createTask}
                  onAssignTask={assignTask}
                  onRemoveTask={removeTask}
                />
              )}
          />
          <Route
            path="/playground"
            element={(
              <PlaygroundPage
                currentUser={currentUser}
                runtime={runtime}
                templates={templates}
                playgroundChallenges={catalog.playgroundChallenges}
                onSaveDraft={saveDraftValue}
                onSelectChallenge={selectChallenge}
                onSelectPlaygroundTemplate={selectPlaygroundTemplate}
                onSelectPlaygroundDataset={selectPlaygroundDataset}
                onRunPlaygroundQuery={runPlaygroundQuery}
                onValidatePlaygroundChallenge={validatePlaygroundChallenge}
              />
            )}
          />
          <Route
            path="/admin"
            element={currentUser.role === 'student'
              ? <Navigate to="/seminar" replace />
              : (
                <AdminPage
                  currentUser={currentUser}
                  runtime={runtime}
                  users={users}
                  groups={groups}
                  templates={templates}
                  onCreateGroup={createGroup}
                  onUpdateGroup={updateGroup}
                  onDeleteGroup={deleteGroup}
                  onCreateUser={createUserRecord}
                  onUpdateUser={updateUserRecord}
                  onDeleteUser={deleteUserRecord}
                  onImportTemplate={importTemplate}
                />
              )}
          />
        </Routes>

        <div className="footer-actions">
          <button className="ghost-button" onClick={resetPlatformState}>
            Очистить состояние
          </button>
        </div>
      </Shell>
    </HashRouter>
  )
}

export default App
