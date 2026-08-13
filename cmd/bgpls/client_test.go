package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyLabDefaultsUsesClabPKI(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("clab/pki", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ca.crt", "admin.crt", "admin.key"} {
		if err := os.WriteFile(filepath.Join("clab/pki", name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := &clientFlags{}
	c.applyLabDefaults()
	if c.ca != "clab/pki/ca.crt" || c.cert != "clab/pki/admin.crt" || c.key != "clab/pki/admin.key" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestApplyLabDefaultsRespectsExplicitFlags(t *testing.T) {
	c := &clientFlags{ca: "other.crt"}
	c.applyLabDefaults()
	if c.ca != "other.crt" || c.cert != "" {
		t.Fatalf("explicit CA was overwritten: %+v", c)
	}
}
