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

type Config struct {
	AppEnv       string
	HTTPAddr     string
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

func (c Config) IsDev() bool { return c.AppEnv == EnvDev }

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

func Load() (Config, error) {
	var (
		cfg  Config
		errs []error
	)

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

func joinLines(errs []error) error {
	msgs := make([]string, len(errs))
	for i, err := range errs {
		msgs[i] = err.Error()
	}
	return errors.New(strings.Join(msgs, "\n  "))
}
