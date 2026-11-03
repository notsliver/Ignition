-- A product's own icon (a preset icon and colour, or an uploaded image), and where and how many
-- of it can run: eligible servers and locations, placement strategy, capacity and stock.
ALTER TABLE products ADD COLUMN icon text NOT NULL DEFAULT '' CHECK (length(icon) <= 400000);
ALTER TABLE products ADD COLUMN placement jsonb NOT NULL DEFAULT '{}';
ALTER TABLE services ADD COLUMN server_id uuid REFERENCES servers (id) ON DELETE SET NULL;
