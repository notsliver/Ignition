-- Server locations (free text such as a region or rack, used for grouping), host facts read by
-- the host probe (CPU model, cores, boot time), and network throughput samples.
ALTER TABLE servers ADD COLUMN location text NOT NULL DEFAULT '' CHECK (length(location) <= 64);
ALTER TABLE servers ADD COLUMN host_facts jsonb NOT NULL DEFAULT '{}';
ALTER TABLE server_metric_samples ADD COLUMN net_rx_bps real, ADD COLUMN net_tx_bps real;
