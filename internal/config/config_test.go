package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func TestLoad_ValidMinimal(t *testing.T) {
	yaml := `
forwards:
  - name: "app"
    listen: ":3000"
    target: "10.0.0.1:3000"
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(cfg.Forwards) != 1 {
		t.Fatalf("expected 1 forward, got %d", len(cfg.Forwards))
	}

	r := cfg.Forwards[0]
	assertEq(t, "name", "app", r.Name)
	assertEq(t, "listen", ":3000", r.Listen)
	assertEq(t, "target", "10.0.0.1:3000", r.Target)

	// Defaults should be applied.
	assertEq(t, "dial_timeout", 10*time.Second, r.DialTimeout)
	assertEq(t, "idle_timeout", 300*time.Second, r.GetIdleTimeout())
	assertEq(t, "max_retries", 3, r.MaxRetries)
	assertEq(t, "retry_interval", 1*time.Second, r.RetryInterval)
	assertEq(t, "keep_alive", 30*time.Second, r.KeepAlive)

	// Global defaults.
	assertEq(t, "log_level", "info", cfg.LogLevel)
	assertEq(t, "log_format", "json", cfg.LogFormat)
	assertEq(t, "hot_reload", true, cfg.HotReload)
	assertEq(t, "shutdown_timeout", 30*time.Second, cfg.ShutdownTimeout)
}

func TestLoad_ValidFull(t *testing.T) {
	yaml := `
log_level: "debug"
log_format: "console"
hot_reload: false
shutdown_timeout: "60s"

forwards:
  - name: "app"
    listen: ":3000"
    target: "10.0.0.1:3000"
    dial_timeout: "5s"
    idle_timeout: "0"
    max_retries: 5
    retry_interval: "500ms"
    keep_alive: "15s"
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	assertEq(t, "log_level", "debug", cfg.LogLevel)
	assertEq(t, "log_format", "console", cfg.LogFormat)
	assertEq(t, "hot_reload", false, cfg.HotReload)
	assertEq(t, "shutdown_timeout", 60*time.Second, cfg.ShutdownTimeout)

	r := cfg.Forwards[0]
	assertEq(t, "dial_timeout", 5*time.Second, r.DialTimeout)
	// idle_timeout: "0" is explicitly set — should be 0 (disabled), NOT the default.
	assertEq(t, "idle_timeout", 0*time.Second, r.GetIdleTimeout())
	assertEq(t, "max_retries", 5, r.MaxRetries)
	assertEq(t, "retry_interval", 500*time.Millisecond, r.RetryInterval)
	assertEq(t, "keep_alive", 15*time.Second, r.KeepAlive)
}

func TestLoad_MultipleRules(t *testing.T) {
	yaml := `
forwards:
  - name: "app"
    listen: ":3000"
    target: "10.0.0.1:3000"
  - name: "redis"
    listen: ":6379"
    target: "10.0.0.1:6379"
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Forwards) != 2 {
		t.Fatalf("expected 2 forwards, got %d", len(cfg.Forwards))
	}
	assertEq(t, "name[0]", "app", cfg.Forwards[0].Name)
	assertEq(t, "name[1]", "redis", cfg.Forwards[1].Name)
}

func TestLoad_NoFile(t *testing.T) {
	_, err := Load("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoad_NoForwards(t *testing.T) {
	yaml := `log_level: "info"`
	path := writeTestConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty forwards")
	}
}

func TestLoad_EmptyName(t *testing.T) {
	yaml := `
forwards:
  - name: ""
    listen: ":3000"
    target: "10.0.0.1:3000"
`
	path := writeTestConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestLoad_DuplicateName(t *testing.T) {
	yaml := `
forwards:
  - name: "app"
    listen: ":3000"
    target: "10.0.0.1:3000"
  - name: "app"
    listen: ":4000"
    target: "10.0.0.1:4000"
`
	path := writeTestConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestLoad_DuplicateListen(t *testing.T) {
	yaml := `
forwards:
  - name: "app1"
    listen: ":3000"
    target: "10.0.0.1:3000"
  - name: "app2"
    listen: ":3000"
    target: "10.0.0.2:3000"
`
	path := writeTestConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate listen address")
	}
}

func TestLoad_InvalidListen(t *testing.T) {
	yaml := `
forwards:
  - name: "app"
    listen: "not-a-valid-addr:sdf"
    target: "10.0.0.1:3000"
`
	path := writeTestConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid listen address")
	}
}

func TestLoad_EmptyTarget(t *testing.T) {
	yaml := `
forwards:
  - name: "app"
    listen: ":3000"
    target: ""
`
	path := writeTestConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty target")
	}
}

func TestLoad_InvalidTarget(t *testing.T) {
	yaml := `
forwards:
  - name: "app"
    listen: ":3000"
    target: "host:notaport"
`
	path := writeTestConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid target address")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTestConfig(t, `{{not yaml`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidate_NegativeTimeouts(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"negative dial_timeout", `
forwards:
  - name: "app"
    listen: ":3000"
    target: "10.0.0.1:3000"
    dial_timeout: "-1s"
`},
		{"negative idle_timeout", `
forwards:
  - name: "app"
    listen: ":3000"
    target: "10.0.0.1:3000"
    idle_timeout: "-1s"
`},
		{"negative max_retries", `
forwards:
  - name: "app"
    listen: ":3000"
    target: "10.0.0.1:3000"
    max_retries: -1
`},
		{"negative keep_alive", `
forwards:
  - name: "app"
    listen: ":3000"
    target: "10.0.0.1:3000"
    keep_alive: "-1s"
`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestConfig(t, tt.yaml)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected error for negative value")
			}
		})
	}
}

func TestApplyRuleDefaults_IdleTimeoutNilGetsDefault(t *testing.T) {
	cfg := &Config{
		Forwards: []ForwardRule{
			{
				Name:        "app",
				Listen:      ":3000",
				Target:      "10.0.0.1:3000",
				IdleTimeout: nil,
			},
		},
	}
	applyRuleDefaults(cfg)
	assertEq(t, "idle_timeout", defaultIdleTimeout, cfg.Forwards[0].GetIdleTimeout())
}

func TestApplyRuleDefaults_IdleTimeoutZeroPreserved(t *testing.T) {
	// User explicitly sets idle_timeout to 0 (disabled) — should NOT be overwritten.
	zero := time.Duration(0)
	cfg := &Config{
		Forwards: []ForwardRule{
			{
				Name:        "app",
				Listen:      ":3000",
				Target:      "10.0.0.1:3000",
				IdleTimeout: &zero,
			},
		},
	}
	applyRuleDefaults(cfg)
	assertEq(t, "idle_timeout", time.Duration(0), cfg.Forwards[0].GetIdleTimeout())
}

func assertEq[T comparable](t *testing.T, field string, want, got T) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %v, got %v", field, want, got)
	}
}
