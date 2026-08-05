-- +goose Up
CREATE SCHEMA platform;

CREATE TABLE platform.account (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      text NOT NULL UNIQUE CHECK (length(username) BETWEEN 1 AND 64),
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- 单用户站点：这张表最多一行，由数据库强制。
-- 常量表达式索引在每一行上都产出同一个键，因此第二行插入必然撞唯一约束。
CREATE UNIQUE INDEX account_singleton ON platform.account ((true));

-- +goose Down
DROP SCHEMA platform CASCADE;
