package config

import "testing"

func TestUIListenMustBeHostPort(t *testing.T) {
	cfg := Default()
	cfg.API.TLS.DevelopmentInsecure = true
	cfg.API.UIListen = "not-an-address"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid ui_listen to be rejected")
	}
	cfg.API.UIListen = "0.0.0.0:8080"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid ui_listen rejected: %v", err)
	}
}

func TestDevelopmentInsecureMustUseLoopback(t *testing.T) {
	cfg := Default()
	cfg.API.Listen = "0.0.0.0:7443"
	cfg.API.TLS.DevelopmentInsecure = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-loopback insecure listener to be rejected")
	}
	cfg.API.Listen = "127.0.0.1:7443"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loopback development listener rejected: %v", err)
	}
}
