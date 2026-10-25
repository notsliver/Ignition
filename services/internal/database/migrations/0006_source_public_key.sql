-- Public half of an SSH key source, shown so it can be added as a deploy key. Not secret.
ALTER TABLE sources ADD COLUMN public_key text NOT NULL DEFAULT '';
