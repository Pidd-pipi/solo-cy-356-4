-- 城市共享菜园管理平台 数据库初始化脚本（基线 DDL，运行时由 GORM AutoMigrate 补齐增量列）
-- 幂等：已存在表则跳过，不会覆盖既有数据。

CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    username VARCHAR(64) NOT NULL,
    CONSTRAINT uni_users_username UNIQUE (username),
    password VARCHAR(128) NOT NULL,
    nickname VARCHAR(64),
    email VARCHAR(128),
    phone VARCHAR(32),
    role VARCHAR(32) NOT NULL DEFAULT 'citizen',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plots (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    code VARCHAR(32) NOT NULL,
    CONSTRAINT uni_plots_code UNIQUE (code),
    area DOUBLE PRECISION NOT NULL,
    soil_type VARCHAR(32) NOT NULL,
    sunlight VARCHAR(32) NOT NULL,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'available',
    adopter_id BIGINT REFERENCES users(id),
    description VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS planting_plans (
    id BIGSERIAL PRIMARY KEY,
    plot_id BIGINT NOT NULL REFERENCES plots(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    crop_name VARCHAR(64) NOT NULL,
    crop_type VARCHAR(32) NOT NULL,
    season VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'planned',
    plant_date TIMESTAMPTZ,
    expected_harvest_date TIMESTAMPTZ,
    notes VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS harvest_records (
    id BIGSERIAL PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES planting_plans(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    crop_name VARCHAR(64) NOT NULL,
    harvest_date TIMESTAMPTZ NOT NULL,
    weight_kg DOUBLE PRECISION NOT NULL,
    quality VARCHAR(32) NOT NULL,
    notes VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS diary_entries (
    id BIGSERIAL PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES planting_plans(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    action_type VARCHAR(32) NOT NULL,
    title VARCHAR(128) NOT NULL,
    content VARCHAR(2000) NOT NULL,
    image_url VARCHAR(512),
    like_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS diary_comments (
    id BIGSERIAL PRIMARY KEY,
    diary_id BIGINT NOT NULL REFERENCES diary_entries(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    content VARCHAR(500) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS community_posts (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    title VARCHAR(128) NOT NULL,
    content VARCHAR(3000) NOT NULL,
    post_type VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    like_count INT NOT NULL DEFAULT 0,
    comment_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS community_comments (
    id BIGSERIAL PRIMARY KEY,
    post_id BIGINT NOT NULL REFERENCES community_posts(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    content VARCHAR(500) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT,
    username VARCHAR(64),
    role VARCHAR(32),
    action VARCHAR(64) NOT NULL,
    resource_type VARCHAR(64),
    resource_id VARCHAR(64),
    detail VARCHAR(1000),
    ip VARCHAR(64),
    request_id VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_plots_status ON plots(status);

-- 地块候补与递补：active_key 仅对有效记录（waiting/invited）为 'active'，
-- 终态记录为 NULL（PG 唯一索引中 NULL 互不相同，终态后可重新候补）。
CREATE TABLE IF NOT EXISTS waitlist_entries (
    id BIGSERIAL PRIMARY KEY,
    plot_id BIGINT NOT NULL REFERENCES plots(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    active_key VARCHAR(16),
    status VARCHAR(32) NOT NULL DEFAULT 'waiting',
    invited_at TIMESTAMPTZ,
    confirm_expires_at TIMESTAMPTZ,
    confirmed_at TIMESTAMPTZ,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    remark VARCHAR(256),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uniq_waitlist_active UNIQUE (plot_id, user_id, active_key)
);

CREATE INDEX IF NOT EXISTS idx_waitlist_queue ON waitlist_entries(plot_id, status, registered_at);
CREATE INDEX IF NOT EXISTS idx_waitlist_user ON waitlist_entries(user_id);
CREATE INDEX IF NOT EXISTS idx_waitlist_expires ON waitlist_entries(confirm_expires_at);
CREATE INDEX IF NOT EXISTS idx_plans_user ON planting_plans(user_id);
CREATE INDEX IF NOT EXISTS idx_plans_status ON planting_plans(status);
CREATE INDEX IF NOT EXISTS idx_harvest_user ON harvest_records(user_id);
CREATE INDEX IF NOT EXISTS idx_diary_user ON diary_entries(user_id);
CREATE INDEX IF NOT EXISTS idx_posts_type ON community_posts(post_type);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(username);
