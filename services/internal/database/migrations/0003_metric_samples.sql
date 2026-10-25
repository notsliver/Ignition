-- Resource usage sampled by the worker every 15 seconds while a container runs.
-- Kept for 7 days; the worker deletes older rows.
CREATE TABLE metric_samples (
    resource_id     uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    at              timestamptz NOT NULL,
    cpu_percent     real NOT NULL,      -- of one CPU core; 100 = one full core
    memory_bytes    bigint NOT NULL,
    memory_limit    bigint NOT NULL,
    net_rx_bps      real,               -- bytes/second since the previous sample; NULL on the first sample after a (re)start
    net_tx_bps      real,
    PRIMARY KEY (resource_id, at)
);
CREATE INDEX metric_samples_at_idx ON metric_samples (at);
