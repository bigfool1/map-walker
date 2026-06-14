CREATE TABLE users (
    id __PK_AUTO__ NOT NULL,
    username VARCHAR(255) NOT NULL,
    username_normalized VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at VARCHAR(25) NOT NULL,
    last_lat DOUBLE,
    last_lng DOUBLE,
    appearance_color VARCHAR(7) NOT NULL DEFAULT '#3388ff',
    appearance_shape VARCHAR(32) NOT NULL DEFAULT 'circle'
);

CREATE TABLE sessions (
    token_hash VARCHAR(64) PRIMARY KEY NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    created_at VARCHAR(25) NOT NULL,
    expires_at VARCHAR(25) NOT NULL
);

CREATE INDEX sessions_user_id ON sessions(user_id);
CREATE INDEX sessions_expires_at ON sessions(expires_at);
