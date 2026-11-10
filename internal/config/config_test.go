package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRepoConfigMatchesDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("QUOTE_SECRET", "")
	t.Setenv("PORT", "")
	got, err := Load("../../config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if want := Default(); !reflect.DeepEqual(got, want) {
		t.Fatalf("config.toml drifted from the defaults:\n got %+v\nwant %+v", got, want)
	}
}

func TestValidateCatchesBadValues(t *testing.T) {
	c := Default()
	c.Dispatch.Policy = "random"
	c.Surge.Max = 0.5
	c.Sim.DecideMaxS = 99
	c.Clock.Start = "25:99"
	err := c.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"dispatch.policy", "surge.max", "decide window", "clock.start"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

func TestUnknownKeysAreRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte("[dispatch]\npolcy = \"batch\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "dispatch.polcy") {
		t.Fatalf("typo should be reported, got %v", err)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("PORT", "7860")
	t.Setenv("QUOTE_SECRET", "s3cret")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.URL != "postgres://x" || c.Server.Addr != ":7860" || c.Server.QuoteSecret != "s3cret" {
		t.Fatalf("env not applied: %+v", c)
	}
}
