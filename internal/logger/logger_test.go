package logger

import (
	"testing"
)

func TestInit_ValidLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error", "DEBUG", "Info", "WARN"}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			err := Init(level, "json")
			if err != nil {
				t.Fatalf("Init(%q, json) error = %v", level, err)
			}
			if L == nil {
				t.Fatal("L should not be nil after Init()")
			}
		})
	}
}

func TestInit_InvalidLevel(t *testing.T) {
	err := Init("invalid-level", "json")
	if err == nil {
		t.Fatal("expected error for invalid level")
	}
}

func TestInit_Formats(t *testing.T) {
	formats := []string{"json", "JSON", "console", "CONSOLE", "any_other"}
	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			err := Init("info", format)
			if err != nil {
				t.Fatalf("Init(info, %q) error = %v", format, err)
			}
			if L == nil {
				t.Fatal("L should not be nil after Init()")
			}
		})
	}
}

func TestInit_LogOutput(t *testing.T) {
	err := Init("info", "json")
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Should not panic when logging.
	L.Info("test message")
	L.Debug("should appear in debug level")
	L.Error("error message")
}

func TestSync_NilLogger(t *testing.T) {
	// Sync should not panic when L is nil.
	L = nil
	Sync()
}

func TestSync_Initialized(t *testing.T) {
	err := Init("info", "json")
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	// Should not panic.
	Sync()
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  string // "ok" or "error"
	}{
		{"debug", "ok"},
		{"info", "ok"},
		{"warn", "ok"},
		{"error", "ok"},
		{"dpanic", "ok"},
		{"panic", "ok"},
		{"fatal", "ok"},
		{"", "ok"},  // zap treats empty string as valid (default level)
		{"invalid", "error"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := parseLevel(tt.input)
			if tt.want == "error" && err == nil {
				t.Errorf("parseLevel(%q): expected error, got nil", tt.input)
			}
			if tt.want == "ok" && err != nil {
				t.Errorf("parseLevel(%q): unexpected error = %v", tt.input, err)
			}
		})
	}
}
