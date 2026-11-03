-- Products: what an instance offers (a game server, a database, an app...), in plans of different
-- sizes and prices. Administrators assign a plan to a team, which provisions a service: one
-- resource, held to the plan's size. Prices are shown, never charged.
CREATE TABLE products (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    category    text NOT NULL DEFAULT '' CHECK (length(category) <= 60),
    kind        text NOT NULL CHECK (kind IN ('application', 'database', 'worker', 'function', 'game')),
    template    jsonb NOT NULL DEFAULT '{}',
    status      text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'retired')),
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE product_plans (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    position   integer NOT NULL DEFAULT 0,
    cpus       numeric(6, 2) NOT NULL DEFAULT 1 CHECK (cpus > 0),
    memory_mb  integer NOT NULL DEFAULT 512 CHECK (memory_mb >= 64),
    storage_gb integer NOT NULL DEFAULT 0 CHECK (storage_gb >= 0), -- 0 = no plan limit
    backup_gb  integer NOT NULL DEFAULT 0 CHECK (backup_gb >= 0),
    ports      integer NOT NULL DEFAULT 0 CHECK (ports >= 0),
    price      text NOT NULL DEFAULT '',  -- shown as entered, e.g. 9.99; empty = no price shown
    currency   text NOT NULL DEFAULT 'USD',
    period     text NOT NULL DEFAULT 'month' CHECK (period IN ('month', 'quarter', 'year', 'once'))
);
CREATE INDEX product_plans_product ON product_plans (product_id, position);

CREATE TABLE services (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id       uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    plan_id          uuid NOT NULL REFERENCES product_plans (id) ON DELETE RESTRICT,
    team_id          uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    resource_id      uuid REFERENCES resources (id) ON DELETE SET NULL,
    name             text NOT NULL,
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'terminated')),
    suspended_reason text NOT NULL DEFAULT '',
    note             text NOT NULL DEFAULT '',
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX services_team ON services (team_id);
CREATE UNIQUE INDEX services_resource ON services (resource_id) WHERE resource_id IS NOT NULL;
