-- "Bring your own files" products: a ready runtime the team fills with Files.
ALTER TABLE products DROP CONSTRAINT products_kind_check;
ALTER TABLE products ADD CONSTRAINT products_kind_check CHECK (kind IN ('application', 'database', 'worker', 'function', 'game', 'files'));
