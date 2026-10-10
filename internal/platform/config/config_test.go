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
