-- Build & Deploy settings that the worker actually applies.
ALTER TABLE resources
    ADD COLUMN build_mode            text    NOT NULL DEFAULT 'auto' CHECK (build_mode IN ('auto', 'dockerfile')),
    ADD COLUMN build_context         text    NOT NULL DEFAULT '',   -- subfolder of the repository; '' = root
    ADD COLUMN build_target          text    NOT NULL DEFAULT '',   -- multi-stage target; '' = last stage
    ADD COLUMN build_args            text[]  NOT NULL DEFAULT '{}', -- NAME=value
    ADD COLUMN build_timeout_minutes integer NOT NULL DEFAULT 30 CHECK (build_timeout_minutes BETWEEN 1 AND 120),
    ADD COLUMN health_seconds        integer NOT NULL DEFAULT 5  CHECK (health_seconds BETWEEN 2 AND 300),
    ADD COLUMN stop_grace_seconds    integer NOT NULL DEFAULT 10 CHECK (stop_grace_seconds BETWEEN 0 AND 300),
    ADD COLUMN release_command       text    NOT NULL DEFAULT '';   -- runs in the new container before traffic switches

ALTER TABLE deployments
    ADD COLUMN no_cache   boolean NOT NULL DEFAULT false,
    ADD COLUMN build_info text    NOT NULL DEFAULT ''; -- how the image was built, e.g. "Dockerfile" or "Detected Node.js (npm)"
