// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package config

import (
	"strings"
	"testing"
)

// XOT248: the default store's /ts stays unprovisioned unless asked otherwise.
func TestTSDefaultProvision_DefaultIsOff(t *testing.T) {
	if got := Default().TSDefaultProvision; got != "off" {
		t.Errorf("TSDefaultProvision = %q, want %q", got, "off")
	}
}

func TestTSDefaultProvision_FromEnvAndValidation(t *testing.T) {
	for _, mode := range []string{"startup", "lazy", "off"} {
		t.Setenv("XOLU_TS_DEFAULT_PROVISION", " "+strings.ToUpper(mode)+" ")
		cfg := Default()
		LoadFromEnv(cfg)
		if cfg.TSDefaultProvision != mode {
			t.Errorf("env %q gave %q", mode, cfg.TSDefaultProvision)
		}
		errs, _ := cfg.Validate()
		if strings.Contains(strings.Join(errs, "\n"), "TSDefaultProvision") {
			t.Errorf("%q must be valid, got %v", mode, errs)
		}
	}
	cfg := Default()
	cfg.TSDefaultProvision = "eventually"
	errs, _ := cfg.Validate()
	if !strings.Contains(strings.Join(errs, "\n"), "TSDefaultProvision") {
		t.Errorf("an unknown mode must be a validation error, got %v", errs)
	}
}

// Commit clock (XOT247) defaults: instants are made durable, but a response only
// carries a clock when templates were used.
func TestCommitClockDefaults(t *testing.T) {
	cfg := Default()
	if !cfg.CommitInstantPersist {
		t.Error("CommitInstantPersist must default to true")
	}
	if cfg.CommitClockAlways {
		t.Error("CommitClockAlways must default to false")
	}
	t.Setenv("XOLU_COMMIT_INSTANT_PERSIST", "false")
	t.Setenv("XOLU_COMMIT_CLOCK_ALWAYS", "true")
	cfg = Default()
	LoadFromEnv(cfg)
	if cfg.CommitInstantPersist || !cfg.CommitClockAlways {
		t.Errorf("env not read: persist=%v always=%v", cfg.CommitInstantPersist, cfg.CommitClockAlways)
	}
}
