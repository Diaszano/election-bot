package main

import (
	"context"
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
