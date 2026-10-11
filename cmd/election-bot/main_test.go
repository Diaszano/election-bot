package main

import (
	"context"
	"strings"
	"testing"
)

func TestCLIWhatsAppModes(t *testing.T) {
	called := ""
	handled, err := dispatchWhatsApp(context.Background(), []string{"whatsapp", "pair"}, func(context.Context) error { called = "pair"; return nil }, func(context.Context) error { called = "logout"; return nil })
	if err != nil || !handled || called != "pair" {
		t.Fatalf("handled=%t called=%q err=%v", handled, called, err)
	}
	handled, err = dispatchWhatsApp(context.Background(), []string{"whatsapp", "logout"}, func(context.Context) error { called = "pair"; return nil }, func(context.Context) error { called = "logout"; return nil })
	if err != nil || !handled || called != "logout" {
		t.Fatalf("handled=%t called=%q err=%v", handled, called, err)
	}
}

func TestCLIVersion(t *testing.T) {
	handled, out, err := dispatchVersion([]string{"version"})
	if !handled || err != nil {
		t.Fatalf("expected handled=true err=nil, got handled=%t err=%v", handled, err)
	}
	if !strings.Contains(out, "election-bot ") {
		t.Fatalf("expected output to contain election-bot, got %q", out)
	}

	handled, outJSON, err := dispatchVersion([]string{"version", "--json"})
	if !handled || err != nil {
		t.Fatalf("expected handled=true err=nil, got handled=%t err=%v", handled, err)
	}
	if !strings.Contains(outJSON, `"version"`) {
		t.Fatalf("expected JSON output to contain \"version\", got %q", outJSON)
	}

	handled, _, _ = dispatchVersion([]string{"other"})
	if handled {
		t.Fatalf("expected handled=false for non-version command")
	}
}
