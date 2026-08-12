package config

import "testing"

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
