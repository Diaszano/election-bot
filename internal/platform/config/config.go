// Package config validates environment settings before local components start.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

type Config struct {
	Environment, LogLevel, Mode, ExecutionMode, Token, Database, Address, ResultsURL, BaseURL, TSEEnvironment, OTLPEndpoint, WhatsAppSessionDB string
	WhatsAppAllowedRecipients                                                                                                                  []subscription.Identity
	TelegramEnabled, OTELEnabled, WhatsAppEnabled, WhatsAppReconnectEnabled, WhatsAppTelemetryEnabled                                          bool
	WhatsAppDigestInterval, WhatsAppEditMinInterval, WhatsAppRecipientMinInterval, WhatsAppConnectTimeout                                      time.Duration
	PollInterval, BeforeInterval, FinalInterval, HTTPTimeout, NotificationInterval, StaleAfter, DelayedAfter, ShutdownTimeout                  time.Duration
	PollTimeout                                                                                                                                int
	Location                                                                                                                                   *time.Location
	CountingStart                                                                                                                              time.Time
	Sampling                                                                                                                                   float64
}

func value(k, d string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return d
}
func Load() (Config, error) { return LoadForMode("runtime") }

func LoadForMode(mode string) (c Config, err error) {
	if mode != "runtime" && mode != "pair" && mode != "logout" && mode != "simulation" {
		return c, fmt.Errorf("invalid execution mode")
	}
	c.ExecutionMode = mode
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
	if mode == "runtime" {
		c.TelegramEnabled, err = strconv.ParseBool(value("TELEGRAM_ENABLED", "false"))
		if err != nil {
			return c, fmt.Errorf("invalid TELEGRAM_ENABLED")
		}
		if c.TelegramEnabled && c.Token == "" {
			return c, fmt.Errorf("TELEGRAM_BOT_TOKEN required when Telegram enabled")
		}
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
	if err = loadWhatsApp(&c, mode); err != nil {
		return c, err
	}
	return c, nil
}

func strictBool(key, fallback string) (bool, error) {
	v := strings.ToLower(value(key, fallback))
	if v == "true" {
		return true, nil
	}
	if v == "false" {
		return false, nil
	}
	return false, fmt.Errorf("invalid %s", key)
}
func loadWhatsApp(c *Config, mode string) error {
	c.WhatsAppSessionDB = value("WHATSAPP_SESSION_DB_PATH", "data/whatsapp-session.db")
	c.WhatsAppDigestInterval = 15 * time.Minute
	c.WhatsAppEditMinInterval = time.Minute
	c.WhatsAppRecipientMinInterval = 3 * time.Second
	c.WhatsAppConnectTimeout = 30 * time.Second
	c.WhatsAppReconnectEnabled = true
	c.WhatsAppTelemetryEnabled = true
	if mode == "simulation" {
		return nil
	}
	if mode == "pair" || mode == "logout" {
		if c.WhatsAppSessionDB == "" {
			return fmt.Errorf("WHATSAPP_SESSION_DB_PATH required")
		}
		var err error
		c.WhatsAppConnectTimeout, err = time.ParseDuration(value("WHATSAPP_CONNECT_TIMEOUT", "30s"))
		if err != nil || c.WhatsAppConnectTimeout <= 0 {
			return fmt.Errorf("invalid WHATSAPP_CONNECT_TIMEOUT")
		}
		return nil
	}
	var err error
	c.WhatsAppEnabled, err = strictBool("WHATSAPP_ENABLED", "false")
	if err != nil {
		return err
	}
	if !c.WhatsAppEnabled {
		return nil
	}
	if c.WhatsAppSessionDB == "" {
		return fmt.Errorf("WHATSAPP_SESSION_DB_PATH required")
	}
	allow := strings.Split(value("WHATSAPP_ALLOWED_RECIPIENTS", ""), ",")
	identity := election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}
	seen := map[string]bool{}
	for _, recipient := range allow {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		id, e := subscription.NewIdentity(identity, subscription.WhatsApp, recipient)
		if e != nil {
			return fmt.Errorf("invalid WHATSAPP_ALLOWED_RECIPIENTS")
		}
		if !seen[id.Key()] {
			seen[id.Key()] = true
			c.WhatsAppAllowedRecipients = append(c.WhatsAppAllowedRecipients, id)
		}
	}
	if len(c.WhatsAppAllowedRecipients) == 0 {
		return fmt.Errorf("WHATSAPP_ALLOWED_RECIPIENTS required when WhatsApp enabled")
	}
	for _, duration := range []struct {
		key, def string
		out      *time.Duration
	}{{"WHATSAPP_DIGEST_INTERVAL", "15m", &c.WhatsAppDigestInterval}, {"WHATSAPP_EDIT_MIN_INTERVAL", "60s", &c.WhatsAppEditMinInterval}, {"WHATSAPP_RECIPIENT_MIN_INTERVAL", "3s", &c.WhatsAppRecipientMinInterval}, {"WHATSAPP_CONNECT_TIMEOUT", "30s", &c.WhatsAppConnectTimeout}} {
		*duration.out, err = time.ParseDuration(value(duration.key, duration.def))
		if err != nil || *duration.out <= 0 {
			return fmt.Errorf("invalid %s", duration.key)
		}
	}
	c.WhatsAppReconnectEnabled, err = strictBool("WHATSAPP_RECONNECT_ENABLED", "true")
	if err != nil {
		return err
	}
	c.WhatsAppTelemetryEnabled, err = strictBool("WHATSAPP_TELEMETRY_ENABLED", "true")
	if err != nil {
		return err
	}
	return nil
}
