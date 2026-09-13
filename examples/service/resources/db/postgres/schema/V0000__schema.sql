-- grant privileges to read-only role
GRANT USAGE ON SCHEMA public to readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO readonly;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO readonly;

-- grant privileges to read-write role
GRANT USAGE ON SCHEMA public to readwrite;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO readwrite;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO readwrite;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO readwrite;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO readwrite;

-- Create the item table, kept in shape parity with the MySQL schema.
-- The Postgres compose services are commented out and this path is untested:
-- the Go repository targets MySQL (see internal/item/repository.go).
CREATE TABLE IF NOT EXISTS item (
    id         UUID         NOT NULL,
    name       VARCHAR(128) NOT NULL,
    quantity   INTEGER      NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uk_item_name UNIQUE (name)
);

CREATE INDEX IF NOT EXISTS idx_item_created_at ON item (created_at, id);
