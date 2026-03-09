package sandbox

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxCPUSeconds != 5 {
		t.Errorf("Expected MaxCPUSeconds=5, got %d", cfg.MaxCPUSeconds)
	}

	if cfg.MaxMemoryBytes != 256*1024*1024 {
		t.Errorf("Expected MaxMemoryBytes=256MB, got %d", cfg.MaxMemoryBytes)
	}

	if cfg.AllowNetwork {
		t.Error("Expected AllowNetwork=false")
	}

	if !cfg.EnableSeccomp {
		t.Error("Expected EnableSeccomp=true")
	}
}

func TestDefaultAllowlistFilter(t *testing.T) {
	filter := DefaultAllowlistFilter()

	if filter.DefaultAction != ActionKill {
		t.Error("Expected default action to be Kill")
	}

	if len(filter.Rules) == 0 {
		t.Error("Expected some allow rules")
	}
}

func TestNetworkDenyFilter(t *testing.T) {
	filter := NetworkDenyFilter()

	if filter.DefaultAction != ActionAllow {
		t.Error("Expected default action to be Allow")
	}

	// All rules should be Kill
	for _, rule := range filter.Rules {
		if rule.Action != ActionKill {
			t.Errorf("Expected rule for syscall %d to be Kill", rule.Syscall)
		}
	}
}
