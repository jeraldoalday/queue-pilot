-- Core tenant, auth, and domain tables for QueuePilot.
-- IDs are UUIDs generated in application code (internal/platform/id), not
-- by the database, so no column defaults are set for primary keys.

CREATE TABLE organizations (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    timezone   TEXT NOT NULL DEFAULT 'UTC',
    plan       TEXT NOT NULL DEFAULT 'free',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email         TEXT NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'staff')),
    disabled_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));
CREATE INDEX users_org_id_idx ON users (org_id);

CREATE TABLE sessions (
    token         TEXT PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    csrf_token    TEXT NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_agent    TEXT,
    ip            TEXT
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_org_id_idx ON sessions (org_id);

CREATE TABLE services (
    id               UUID PRIMARY KEY,
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug             TEXT NOT NULL,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes > 0),
    price_cents      INTEGER,
    active           BOOLEAN NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX services_org_id_idx ON services (org_id);

CREATE TABLE staff_services (
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    staff_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    service_id    UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    PRIMARY KEY (staff_user_id, service_id)
);
CREATE INDEX staff_services_org_id_idx ON staff_services (org_id);
CREATE INDEX staff_services_service_id_idx ON staff_services (service_id);

CREATE TABLE staff_availability (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    staff_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    weekday       SMALLINT NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_time    TIME NOT NULL,
    end_time      TIME NOT NULL CHECK (end_time > start_time),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX staff_availability_org_id_idx ON staff_availability (org_id);
CREATE INDEX staff_availability_staff_user_id_idx ON staff_availability (staff_user_id, weekday);

CREATE TABLE appointments (
    id             UUID PRIMARY KEY,
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    service_id     UUID NOT NULL REFERENCES services(id) ON DELETE RESTRICT,
    staff_user_id  UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    customer_name  TEXT NOT NULL,
    customer_email TEXT NOT NULL,
    customer_phone TEXT,
    starts_at      TIMESTAMPTZ NOT NULL,
    ends_at        TIMESTAMPTZ NOT NULL CHECK (ends_at > starts_at),
    status         TEXT NOT NULL DEFAULT 'booked'
                       CHECK (status IN ('booked', 'cancelled', 'completed', 'no_show')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX appointments_org_id_idx ON appointments (org_id);
CREATE INDEX appointments_staff_starts_idx ON appointments (staff_user_id, starts_at);

CREATE TABLE queues (
    id         UUID PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug       TEXT NOT NULL,
    name       TEXT NOT NULL,
    active     BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX queues_org_id_idx ON queues (org_id);

CREATE TABLE queue_daily_counters (
    queue_id    UUID NOT NULL REFERENCES queues(id) ON DELETE CASCADE,
    ticket_date DATE NOT NULL,
    last_number INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (queue_id, ticket_date)
);

CREATE TABLE queue_tickets (
    id                UUID PRIMARY KEY,
    org_id            UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    queue_id          UUID NOT NULL REFERENCES queues(id) ON DELETE CASCADE,
    number            INTEGER NOT NULL,
    customer_name     TEXT NOT NULL,
    customer_phone    TEXT,
    status            TEXT NOT NULL DEFAULT 'waiting'
                          CHECK (status IN ('waiting', 'called', 'serving', 'done', 'no_show', 'cancelled')),
    called_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    called_at         TIMESTAMPTZ,
    serving_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ
);
CREATE INDEX queue_tickets_org_id_idx ON queue_tickets (org_id);
CREATE INDEX queue_tickets_queue_status_number_idx ON queue_tickets (queue_id, status, number);
