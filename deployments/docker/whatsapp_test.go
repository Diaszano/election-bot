package docker

import (
	"os"
	"strings"
	"testing"
)

func readRootFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWhatsAppComposeUsesExistingProtectedApplication(t *testing.T) {
	compose := readRootFile(t, "compose.yaml")
	for _, setting := range []string{"WHATSAPP_ENABLED", "WHATSAPP_SESSION_DB_PATH", "WHATSAPP_ALLOWED_RECIPIENTS", "WHATSAPP_DIGEST_INTERVAL", "WHATSAPP_EDIT_MIN_INTERVAL", "WHATSAPP_RECIPIENT_MIN_INTERVAL", "WHATSAPP_CONNECT_TIMEOUT", "WHATSAPP_RECONNECT_ENABLED", "WHATSAPP_TELEMETRY_ENABLED"} {
		if !strings.Contains(compose, setting+":") {
			t.Errorf("Compose does not forward %s", setting)
		}
	}
	if !strings.Contains(compose, "election-data:/data") || strings.Contains(compose, "whatsapp:") || !strings.Contains(compose, "WHATSAPP_SESSION_DB_PATH:-/data/whatsapp-session.db") || !strings.Contains(compose, "SQLITE_PATH: /data/elections.db") || strings.Contains(compose, "WHATSAPP_SESSION_DB_PATH: /data/elections.db") {
		t.Fatal("WhatsApp must use the existing persistent volume and its own database")
	}
	if !strings.Contains(compose, "TELEGRAM_ENABLED") || !strings.Contains(compose, "TELEGRAM_BOT_TOKEN") || !strings.Contains(compose, "ports:") {
		t.Fatal("existing Telegram environment or application port was removed")
	}
	if !strings.Contains(readRootFile(t, "Dockerfile"), `ENTRYPOINT ["/election-bot"]`) || !strings.Contains(readRootFile(t, "Dockerfile"), "USER 65532:65532") {
		t.Fatal("pair/logout CLI or non-root image entrypoint was changed")
	}
}

func TestWhatsAppSessionAndSecretsAreExcluded(t *testing.T) {
	for _, file := range []string{".dockerignore", ".gitignore"} {
		contents := readRootFile(t, file)
		for _, pattern := range []string{".env", "whatsapp-session.db*", "*.session.lock"} {
			if !strings.Contains(contents, pattern) {
				t.Errorf("%s does not exclude %s", file, pattern)
			}
		}
	}
}

func TestWhatsAppComposeConfigurationDefaultsDisabled(t *testing.T) {
	if !strings.Contains(readRootFile(t, ".env.example"), "WHATSAPP_ENABLED=false") {
		t.Fatal("WhatsApp must stay disabled by default")
	}
}
