// Package config validates environment settings before local components start.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment, LogLevel, Mode, Token, Database, Address, ResultsURL, BaseURL, TSEEnvironment, OTLPEndpoint                  string
	TelegramEnabled, OTELEnabled                                                                                              bool
	PollInterval, BeforeInterval, FinalInterval, HTTPTimeout, NotificationInterval, StaleAfter, DelayedAfter, ShutdownTimeout time.Duration
	PollTimeout                                                                                                               int
	Location                                                                                                                  *time.Location
	CountingStart                                                                                                             time.Time
	Sampling                                                                                                                  float64
}

func value(k, d string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return d
}
func Load() (c Config, err error) {
	for k, want := range map[string]string{"APP_NAME": "election-bot", "OTEL_SERVICE_NAME": "election-bot", "OTEL_SERVICE_NAMESPACE": "elections", "ELECTION_YEAR": "2026", "ELECTION_ROUND": "2", "ELECTION_CODE": "6258"} {
		if value(k, want) != want {
			return c, fmt.Errorf("%s must be %s", k, want)
		}
	}
	c.Environment = value("APP_ENV", "development")
	c.LogLevel = strings.ToUpper(value("APP_LOG_LEVEL", "INFO"))
	if !strings.Contains("|DEBUG|INFO|WARN|ERROR|", "|"+c.LogLevel+"|") {
		return c, fmt.Errorf("invalid APP_LOG_LEVEL")
	}
	c.Mode = value("DATA_SOURCE_MODE", "simulation")
	if c.Mode != "simulation" {
		return c, fmt.Errorf("official production contract unverified; DATA_SOURCE_MODE must be simulation")
	}
	c.Token = value("TELEGRAM_BOT_TOKEN", "")
	c.TelegramEnabled, err = strconv.ParseBool(value("TELEGRAM_ENABLED", "false"))
	if err != nil {
		return c, fmt.Errorf("invalid TELEGRAM_ENABLED")
	}
	if c.TelegramEnabled && c.Token == "" {
		return c, fmt.Errorf("TELEGRAM_BOT_TOKEN required when Telegram enabled")
	}
	c.OTELEnabled, err = strconv.ParseBool(value("OTEL_ENABLED", "false"))
	if err != nil {
		return c, fmt.Errorf("invalid OTEL_ENABLED")
	}
	c.Database = value("SQLITE_PATH", "data/elections.db")
	if c.Database == "" {
		return c, fmt.Errorf("SQLITE_PATH required")
	}
	c.Address = value("HTTP_LISTEN_ADDRESS", ":8080")
	if c.Address == "" {
		return c, fmt.Errorf("HTTP_LISTEN_ADDRESS required")
	}
	c.BaseURL = value("TSE_BASE_URL", "https://resultados.tse.jus.br")
	c.ResultsURL = value("TSE_RESULTS_URL", "")
	c.TSEEnvironment = value("TSE_ENVIRONMENT", "oficial")
	c.OTLPEndpoint = value("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
	for k, v := range map[string]string{"TSE_BASE_URL": c.BaseURL, "TSE_RESULTS_URL": c.ResultsURL, "OTEL_EXPORTER_OTLP_ENDPOINT": c.OTLPEndpoint} {
		if v == "" && k == "TSE_RESULTS_URL" {
			continue
		}
		u, e := url.Parse(v)
		if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return c, fmt.Errorf("invalid %s", k)
		}
	}
	if value("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc") != "grpc" {
		return c, fmt.Errorf("only OTLP grpc supported")
	}
	if value("OTEL_TRACES_SAMPLER", "parentbased_traceidratio") != "parentbased_traceidratio" {
		return c, fmt.Errorf("unsupported sampler")
	}
	c.Sampling, err = strconv.ParseFloat(value("OTEL_TRACES_SAMPLER_ARG", "1.0"), 64)
	if err != nil || !(c.Sampling >= 0 && c.Sampling <= 1) {
		return c, fmt.Errorf("invalid sampling ratio")
	}
	c.Location, err = time.LoadLocation(value("APP_TIMEZONE", "America/Sao_Paulo"))
	if err != nil {
		return c, fmt.Errorf("invalid APP_TIMEZONE")
	}
	c.CountingStart, err = time.Parse(time.RFC3339, value("ELECTION_COUNTING_START", "2026-10-25T17:00:00-03:00"))
	if err != nil {
		return c, fmt.Errorf("invalid counting window")
	}
	c.PollTimeout, err = strconv.Atoi(value("TELEGRAM_POLL_TIMEOUT", "30"))
	if err != nil || c.PollTimeout < 1 || c.PollTimeout > 50 {
		return c, fmt.Errorf("invalid TELEGRAM_POLL_TIMEOUT")
	}
	for _, d := range []struct {
		k, v string
		p    *time.Duration
	}{{"TSE_POLL_INTERVAL", "10s", &c.PollInterval}, {"TSE_PRE_COUNTING_INTERVAL", "5m", &c.BeforeInterval}, {"TSE_FINAL_INTERVAL", "1h", &c.FinalInterval}, {"TSE_HTTP_TIMEOUT", "5s", &c.HTTPTimeout}, {"NOTIFICATION_INTERVAL", "1s", &c.NotificationInterval}, {"SYNC_STALE_AFTER", "2m", &c.StaleAfter}, {"PUBLICATION_DELAYED_AFTER", "5m", &c.DelayedAfter}, {"SHUTDOWN_TIMEOUT", "10s", &c.ShutdownTimeout}} {
		*d.p, err = time.ParseDuration(value(d.k, d.v))
		if err != nil || *d.p <= 0 {
			return c, fmt.Errorf("invalid %s", d.k)
		}
	}
	return c, nil
}
