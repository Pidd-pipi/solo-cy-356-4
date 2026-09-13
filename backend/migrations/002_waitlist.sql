-- 002_waitlist.sql 地块候补与递补
-- 与 database/init.sql 中 waitlist_entries 段保持一致；运行时由 GORM AutoMigrate 补齐。
-- active_key 仅对有效记录（waiting/invited）为 'active'，终态为 NULL，
-- 借助 PG 唯一索引 NULL 互不相同实现“同一人同一地块仅一条有效记录”。

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
