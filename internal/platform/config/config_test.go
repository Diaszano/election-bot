package config

import "testing"

func TestOfflineSimulationNeedsNoToken(t *testing.T) {
	t.Setenv("TELEGRAM_ENABLED", "false")
	c, e := Load()
	if e != nil || c.Mode != "simulation" {
		t.Fatalf("%+v %v", c, e)
	}
}
func TestInvalidConfiguration(t *testing.T) {
	for _, v := range []struct{ k, v string }{{"TSE_POLL_INTERVAL", "0s"}, {"APP_TIMEZONE", "bad/zone"}, {"OTEL_TRACES_SAMPLER_ARG", "2"}, {"APP_NAME", "other"}, {"ELECTION_CODE", "21270"}, {"DATA_SOURCE_MODE", "bad"}, {"TSE_BASE_URL", "ftp://bad"}, {"TELEGRAM_ENABLED", "true"}} {
		t.Run(v.k, func(t *testing.T) {
			t.Setenv("TELEGRAM_BOT_TOKEN", "")
			t.Setenv(v.k, v.v)
			if _, e := Load(); e == nil {
				t.Fatal("accepted invalid setting")
			}
		})
	}
}
func TestOfficialModeFailsClosed(t *testing.T) {
	t.Setenv("DATA_SOURCE_MODE", "official")
	if _, e := Load(); e == nil {
		t.Fatal("unverified official contract enabled")
	}
}

func TestWhatsAppDisabledConfiguration(t *testing.T) {
	t.Setenv("WHATSAPP_ENABLED", "false")
	t.Setenv("WHATSAPP_ALLOWED_RECIPIENTS", "")
	c, err := LoadForMode("runtime")
	if err != nil || c.WhatsAppEnabled {
		t.Fatalf("config=%+v err=%v", c, err)
	}
}

func TestPairModeAllowsEmptyAllowlist(t *testing.T) {
	t.Setenv("WHATSAPP_ALLOWED_RECIPIENTS", "")
	if _, err := LoadForMode("pair"); err != nil {
		t.Fatal(err)
	}
}

func TestEnabledWhatsAppRequiresAllowlistAndStrictBooleans(t *testing.T) {
	t.Setenv("WHATSAPP_ENABLED", "true")
	t.Setenv("WHATSAPP_ALLOWED_RECIPIENTS", "")
	if _, err := LoadForMode("runtime"); err == nil {
		t.Fatal("empty WhatsApp allowlist accepted")
	}
	t.Setenv("WHATSAPP_ENABLED", "1")
	if _, err := LoadForMode("runtime"); err == nil {
		t.Fatal("non-strict WhatsApp boolean accepted")
	}
}

func TestOfflineSimulationIgnoresWhatsAppSettings(t *testing.T) {
	t.Setenv("WHATSAPP_ENABLED", "not-a-boolean")
	t.Setenv("WHATSAPP_ALLOWED_RECIPIENTS", "")
	c, err := LoadForMode("simulation")
	if err != nil || c.WhatsAppEnabled {
		t.Fatalf("config=%+v err=%v", c, err)
	}
}
