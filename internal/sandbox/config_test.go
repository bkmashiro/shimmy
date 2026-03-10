package sandbox

import "testing"

func TestDefaultConfig_MaxMemoryMB(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.MaxMemoryMB != 256 {
		t.Fatalf("MaxMemoryMB = %d, want 256", cfg.MaxMemoryMB)
	}
}

func TestDefaultConfig_CPUTimeSecs(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.CPUTimeSecs != 10 {
		t.Fatalf("CPUTimeSecs = %d, want 10", cfg.CPUTimeSecs)
	}
}

func TestDefaultConfig_AllowNetwork(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.AllowNetwork {
		t.Fatal("AllowNetwork = true, want false")
	}
}

func TestDefaultConfig_WorkDir(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.WorkDir != "" {
		t.Fatalf("WorkDir = %q, want empty", cfg.WorkDir)
	}
}

func TestDefaultConfig_EnvVars(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.EnvVars != nil {
		t.Fatalf("EnvVars = %v, want nil", cfg.EnvVars)
	}
}

func TestDefaultConfig_IsImmutable(t *testing.T) {
	t.Parallel()

	cfg1 := DefaultConfig()
	cfg1.MaxMemoryMB = 9999
	cfg1.CPUTimeSecs = 9999
	cfg1.AllowNetwork = true

	cfg2 := DefaultConfig()
	if cfg2.MaxMemoryMB != 256 {
		t.Fatal("DefaultConfig() not returning fresh values")
	}
	if cfg2.CPUTimeSecs != 10 {
		t.Fatal("DefaultConfig() not returning fresh values")
	}
	if cfg2.AllowNetwork {
		t.Fatal("DefaultConfig() not returning fresh values")
	}
}

func TestConfig_ZeroValue(t *testing.T) {
	t.Parallel()

	var cfg Config
	if cfg.MaxMemoryMB != 0 {
		t.Fatalf("zero Config.MaxMemoryMB = %d, want 0", cfg.MaxMemoryMB)
	}
	if cfg.CPUTimeSecs != 0 {
		t.Fatalf("zero Config.CPUTimeSecs = %d, want 0", cfg.CPUTimeSecs)
	}
	if cfg.AllowNetwork {
		t.Fatal("zero Config.AllowNetwork = true, want false")
	}
	if cfg.WorkDir != "" {
		t.Fatalf("zero Config.WorkDir = %q, want empty", cfg.WorkDir)
	}
	if cfg.EnvVars != nil {
		t.Fatalf("zero Config.EnvVars = %v, want nil", cfg.EnvVars)
	}
}

func TestConfig_CustomValues(t *testing.T) {
	t.Parallel()

	cfg := Config{
		MaxMemoryMB:  1024,
		CPUTimeSecs:  60,
		AllowNetwork: true,
		WorkDir:      "/custom/dir",
		EnvVars:      []string{"FOO=bar", "BAZ=qux"},
	}

	if cfg.MaxMemoryMB != 1024 {
		t.Fatalf("MaxMemoryMB = %d, want 1024", cfg.MaxMemoryMB)
	}
	if cfg.CPUTimeSecs != 60 {
		t.Fatalf("CPUTimeSecs = %d, want 60", cfg.CPUTimeSecs)
	}
	if !cfg.AllowNetwork {
		t.Fatal("AllowNetwork = false, want true")
	}
	if cfg.WorkDir != "/custom/dir" {
		t.Fatalf("WorkDir = %q, want %q", cfg.WorkDir, "/custom/dir")
	}
	if len(cfg.EnvVars) != 2 {
		t.Fatalf("len(EnvVars) = %d, want 2", len(cfg.EnvVars))
	}
}

func TestConfig_LargeMemoryValue(t *testing.T) {
	t.Parallel()
	cfg := Config{MaxMemoryMB: 65536} // 64GB
	if cfg.MaxMemoryMB != 65536 {
		t.Fatalf("MaxMemoryMB = %d, want 65536", cfg.MaxMemoryMB)
	}
}

func TestConfig_NegativeMemoryNotPrevented(t *testing.T) {
	t.Parallel()
	// Config struct doesn't validate - the backend decides how to handle
	cfg := Config{MaxMemoryMB: -1}
	if cfg.MaxMemoryMB != -1 {
		t.Fatalf("MaxMemoryMB = %d, want -1", cfg.MaxMemoryMB)
	}
}

func TestConfig_NegativeCPUNotPrevented(t *testing.T) {
	t.Parallel()
	cfg := Config{CPUTimeSecs: -1}
	if cfg.CPUTimeSecs != -1 {
		t.Fatalf("CPUTimeSecs = %d, want -1", cfg.CPUTimeSecs)
	}
}

func TestConfig_EmptyEnvVars(t *testing.T) {
	t.Parallel()
	cfg := Config{EnvVars: []string{}}
	if cfg.EnvVars == nil {
		t.Fatal("EnvVars = nil, want empty slice")
	}
	if len(cfg.EnvVars) != 0 {
		t.Fatalf("len(EnvVars) = %d, want 0", len(cfg.EnvVars))
	}
}
