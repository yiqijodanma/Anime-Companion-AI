CREATE TABLE users (
    id varchar(32) PRIMARY KEY,
    email varchar(320) NOT NULL UNIQUE,
    password_hash text NOT NULL,
    verified_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_email ON users (email);
