-- The product is written "Ignition". Fix the local server's description stored by 0015, unless
-- someone already changed it.
UPDATE servers SET description = 'The Docker host Ignition runs on. Deployments run here.'
WHERE is_local AND description = 'The Docker host IGNITION runs on. Deployments run here.';
