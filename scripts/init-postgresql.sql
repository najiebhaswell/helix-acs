-- scripts/init-postgresql.sql
-- Initialization script untuk PostgreSQL Helix ACS

-- Grant permissions
GRANT CONNECT ON DATABASE helix_parameters TO helix;
GRANT USAGE ON SCHEMA public TO helix;
GRANT CREATE ON SCHEMA public TO helix;

-- Set default privileges
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO helix;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO helix;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO helix;
