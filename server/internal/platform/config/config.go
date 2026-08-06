// Package config 把进程环境变量读成一个 Config。
//
// 三条不可动摇的规则：
//   - 全部来自环境变量，没有前缀；
//   - 没有任何默认值，缺一个就启动失败；
//   - DSN 只在这里拼装一次，别处一律取 Config.DSN()。
//
// 二进制本身不认识 .env 文件——本地由 Makefile include 并 export，生产由 compose
// 注入。两边都只经过「进程环境变量」这一条路径，少一种「本地能跑、线上不行」的原因。
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是整个进程的配置。字段全部是已经校验过的值：SiteTimezone 一定加载得出来，
// AppEnv 一定是 dev 或 prod，DBPort 一定是个合法端口。
type Config struct {
	// AppEnv 是 "dev" 或 "prod"，决定 slog 用 Text 还是 JSON handler。
	AppEnv string
	// HTTPAddr 是 Echo 的监听地址，例如 ":8080"。
	HTTPAddr string
	// SiteTimezone 是「今天」「本月」的唯一裁决依据。数据库里一律存 UTC。
	SiteTimezone *time.Location

	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string
}

const (
	EnvDev  = "dev"
	EnvProd = "prod"
)

// IsDev 报告是否运行在开发环境。
func (c Config) IsDev() bool { return c.AppEnv == EnvDev }

// DSN 拼装 Postgres 连接串。这是全站唯一的拼装处。
//
// sslmode=disable 是刻意的：本地是回环连接，生产上应用与数据库同处一个 docker
// 网络、不经过公网。哪一天数据库搬到了另一台机器上，这里要跟着改，并且那时它值得
// 成为一个独立的配置项。
func (c Config) DSN() string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.DBUser, c.DBPassword),
		Host:     net.JoinHostPort(c.DBHost, strconv.Itoa(c.DBPort)),
		Path:     "/" + c.DBName,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

// Load 从环境变量读出配置。任何一个变量缺失或非法都会返回 error，且 error 里会
// 一次性列出**全部**问题——只报第一个会让人陷入「改一个、再跑、再报下一个」的循环。
func Load() (Config, error) {
	var (
		cfg  Config
		errs []error
	)

	// req 取一个必填变量。缺失或为空都算缺失：空字符串对这里的每一个变量都没有意义，
	// 而 `DB_PASSWORD=` 这种写法几乎总是笔误。
	req := func(key string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			errs = append(errs, fmt.Errorf("缺少环境变量 %s", key))
		}
		return v
	}

	cfg.AppEnv = req("APP_ENV")
	if cfg.AppEnv != "" && cfg.AppEnv != EnvDev && cfg.AppEnv != EnvProd {
		errs = append(errs, fmt.Errorf("APP_ENV 只能是 %q 或 %q，得到 %q", EnvDev, EnvProd, cfg.AppEnv))
	}

	cfg.HTTPAddr = req("HTTP_ADDR")

	// 时区在这里就解析掉，而不是留给业务代码——一个拼错的时区名应当在启动那一秒
	// 就炸掉，而不是等到某个月末结算去算「本月」的时候。
	if tz := req("SITE_TIMEZONE"); tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			errs = append(errs, fmt.Errorf("SITE_TIMEZONE %q 无法解析：%w", tz, err))
		}
		cfg.SiteTimezone = loc
	}

	cfg.DBHost = req("DB_HOST")
	if port := req("DB_PORT"); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Errorf("DB_PORT %q 不是合法端口", port))
		}
		cfg.DBPort = n
	}
	cfg.DBName = req("DB_NAME")
	cfg.DBUser = req("DB_USER")
	cfg.DBPassword = req("DB_PASSWORD")

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("配置不完整：\n  %w", joinLines(errs))
	}
	return cfg, nil
}

// joinLines 把多个 error 拼成每行一条，便于人眼扫描。
func joinLines(errs []error) error {
	msgs := make([]string, len(errs))
	for i, err := range errs {
		msgs[i] = err.Error()
	}
	return errors.New(strings.Join(msgs, "\n  "))
}
