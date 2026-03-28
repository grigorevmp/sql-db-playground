-- SQL Seminar Platform: PostgreSQL schema

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ─── Lookup types ────────────────────────────────────────────────────────────

CREATE TYPE user_role AS ENUM ('student', 'teacher', 'admin');
CREATE TYPE seminar_status AS ENUM ('scheduled', 'live', 'closed');
CREATE TYPE validation_mode AS ENUM ('result-match', 'ddl-object', 'explain-plan');
CREATE TYPE difficulty AS ENUM ('easy', 'medium', 'hard');
CREATE TYPE feedback_mode AS ENUM ('full', 'preview', 'row-count', 'match-only');
CREATE TYPE query_context AS ENUM ('seminar', 'playground');
CREATE TYPE submission_status AS ENUM ('correct', 'incorrect', 'runtime-error', 'blocked');
CREATE TYPE notification_level AS ENUM ('info', 'success', 'warning');

-- ─── Users & Groups ──────────────────────────────────────────────────────────

CREATE TABLE groups (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    stream     TEXT NOT NULL
);

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    full_name     TEXT NOT NULL,
    login         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          user_role NOT NULL,
    group_id      TEXT REFERENCES groups(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── DB Templates ────────────────────────────────────────────────────────────

CREATE TABLE db_templates (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT NOT NULL,
    level       difficulty NOT NULL,
    topics      TEXT[] NOT NULL DEFAULT '{}',
    tables_json JSONB NOT NULL DEFAULT '[]',  -- []TableDefinition
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE template_datasets (
    id          TEXT PRIMARY KEY,
    template_id TEXT NOT NULL REFERENCES db_templates(id) ON DELETE CASCADE,
    label       TEXT NOT NULL,
    description TEXT NOT NULL,
    schema_sql  TEXT,
    seed_sql    TEXT,
    init_sql    TEXT NOT NULL,
    sort_order  INT NOT NULL DEFAULT 0
);

-- ─── Seminars ────────────────────────────────────────────────────────────────

CREATE TABLE seminars (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT NOT NULL,
    group_id    TEXT NOT NULL REFERENCES groups(id),
    teacher_id  TEXT NOT NULL REFERENCES users(id),
    template_id TEXT NOT NULL REFERENCES db_templates(id),
    access_code TEXT NOT NULL,
    start_time  TIMESTAMPTZ NOT NULL,
    end_time    TIMESTAMPTZ NOT NULL,
    status      seminar_status NOT NULL DEFAULT 'scheduled',
    -- SeminarSettings
    leaderboard_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    auto_validation_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    notifications_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    diagnostics_visible    BOOLEAN NOT NULL DEFAULT TRUE,
    submissions_frozen     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- many-to-many: seminar <-> tasks (ordered)
CREATE TABLE seminar_tasks (
    seminar_id TEXT NOT NULL REFERENCES seminars(id) ON DELETE CASCADE,
    task_id    TEXT NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    PRIMARY KEY (seminar_id, task_id)
);

-- many-to-many: seminar <-> students
CREATE TABLE seminar_students (
    seminar_id TEXT NOT NULL REFERENCES seminars(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (seminar_id, user_id)
);

-- ─── Tasks ───────────────────────────────────────────────────────────────────

CREATE TABLE tasks (
    id                TEXT PRIMARY KEY,
    seminar_id        TEXT NOT NULL REFERENCES seminars(id) ON DELETE CASCADE,
    title             TEXT NOT NULL,
    description       TEXT NOT NULL,
    difficulty        difficulty NOT NULL,
    task_type         TEXT NOT NULL,
    constructs        TEXT[] NOT NULL DEFAULT '{}',
    validation_mode   validation_mode NOT NULL DEFAULT 'result-match',
    template_id       TEXT NOT NULL REFERENCES db_templates(id),
    dataset_ids       TEXT[] NOT NULL DEFAULT '{}',
    starter_sql       TEXT NOT NULL DEFAULT '',
    expected_query    TEXT NOT NULL,
    -- ValidationConfig
    order_matters      BOOLEAN NOT NULL DEFAULT TRUE,
    column_names_matter BOOLEAN NOT NULL DEFAULT TRUE,
    numeric_tolerance  DOUBLE PRECISION NOT NULL DEFAULT 0.001,
    max_execution_ms   INT NOT NULL DEFAULT 5000,
    max_result_rows    INT NOT NULL DEFAULT 100,
    forbidden_keywords TEXT[] NOT NULL DEFAULT '{}',
    -- ValidationSpec (optional JSON for DDL/EXPLAIN tasks)
    validation_spec   JSONB,
    hints             TEXT[] NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Playground Challenges ───────────────────────────────────────────────────

CREATE TABLE playground_challenges (
    id              TEXT PRIMARY KEY,
    template_id     TEXT NOT NULL REFERENCES db_templates(id),
    title           TEXT NOT NULL,
    description     TEXT NOT NULL,
    difficulty      difficulty NOT NULL,
    topic           TEXT NOT NULL,
    constructs      TEXT[] NOT NULL DEFAULT '{}',
    dataset_ids     TEXT[] NOT NULL DEFAULT '{}',
    starter_sql     TEXT NOT NULL DEFAULT '',
    expected_query  TEXT NOT NULL,
    feedback_mode   feedback_mode NOT NULL DEFAULT 'full',
    validation_mode validation_mode,
    validation_spec JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Query Runs ──────────────────────────────────────────────────────────────

CREATE TABLE query_runs (
    id                      TEXT PRIMARY KEY,
    user_id                 TEXT NOT NULL REFERENCES users(id),
    role                    user_role NOT NULL,
    context                 query_context NOT NULL,
    seminar_id              TEXT REFERENCES seminars(id),
    playground_challenge_id TEXT REFERENCES playground_challenges(id),
    task_id                 TEXT,
    dataset_id              TEXT NOT NULL,
    sql_text                TEXT NOT NULL,
    status                  TEXT NOT NULL,
    execution_time_ms       INT NOT NULL DEFAULT 0,
    row_count               INT NOT NULL DEFAULT 0,
    result_json             JSONB,  -- QueryResultTable
    error_message           TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_query_runs_user_id ON query_runs(user_id);
CREATE INDEX idx_query_runs_seminar_id ON query_runs(seminar_id);

-- ─── Submissions ─────────────────────────────────────────────────────────────

CREATE TABLE submissions (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users(id),
    seminar_id         TEXT NOT NULL REFERENCES seminars(id),
    task_id            TEXT NOT NULL,
    sql_text           TEXT NOT NULL,
    submitted_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status             submission_status NOT NULL,
    execution_time_ms  INT NOT NULL DEFAULT 0,
    validation_details JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_submissions_user_task ON submissions(user_id, task_id);
CREATE INDEX idx_submissions_seminar ON submissions(seminar_id);

-- ─── Notifications ───────────────────────────────────────────────────────────

CREATE TABLE notifications (
    id         TEXT PRIMARY KEY,
    seminar_id TEXT NOT NULL REFERENCES seminars(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    level      notification_level NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL
);

CREATE INDEX idx_notifications_seminar ON notifications(seminar_id, created_at DESC);

-- ─── Event Logs ──────────────────────────────────────────────────────────────

CREATE TABLE event_logs (
    id                      TEXT PRIMARY KEY,
    user_id                 TEXT NOT NULL REFERENCES users(id),
    role                    user_role NOT NULL,
    session_id              TEXT NOT NULL,
    seminar_id              TEXT REFERENCES seminars(id),
    playground_challenge_id TEXT REFERENCES playground_challenges(id),
    task_id                 TEXT,
    event_type              TEXT NOT NULL,
    sql_text                TEXT,
    status                  TEXT,
    execution_time_ms       INT,
    payload                 JSONB NOT NULL DEFAULT '{}',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_event_logs_seminar ON event_logs(seminar_id, created_at DESC);
CREATE INDEX idx_event_logs_user ON event_logs(user_id, created_at DESC);

-- ─── Per-user state ──────────────────────────────────────────────────────────

CREATE TABLE user_seminar_selections (
    user_id    TEXT NOT NULL REFERENCES users(id),
    seminar_id TEXT NOT NULL REFERENCES seminars(id),
    task_id    TEXT,
    PRIMARY KEY (user_id, seminar_id)
);

CREATE TABLE user_playground_selections (
    user_id                         TEXT PRIMARY KEY REFERENCES users(id),
    selected_playground_template_id TEXT,
    selected_playground_challenge_id TEXT,
    selected_playground_dataset_id  TEXT
);

CREATE TABLE user_drafts (
    user_id    TEXT NOT NULL REFERENCES users(id),
    draft_key  TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, draft_key)
);

-- ─── Seminar runtime overrides ────────────────────────────────────────────────

CREATE TABLE seminar_runtime (
    seminar_id              TEXT PRIMARY KEY REFERENCES seminars(id) ON DELETE CASCADE,
    status                  seminar_status NOT NULL,
    leaderboard_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    auto_validation_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    notifications_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    diagnostics_visible     BOOLEAN NOT NULL DEFAULT TRUE,
    submissions_frozen      BOOLEAN NOT NULL DEFAULT FALSE,
    last_picked_student_id  TEXT REFERENCES users(id)
);
