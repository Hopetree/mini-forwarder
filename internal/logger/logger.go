// Package logger provides a global zap logger with configurable level and format.
//
// Usage:
//
//	logger.Init("info", "json")
//	defer logger.Sync()
//	logger.L.Info("server started")
package logger

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// L is the global logger instance. Must be initialized via Init() before use.
var L *zap.Logger

// Init initializes the global logger with the specified level and format.
//
// Parameters:
//   - level: log level, one of "debug", "info", "warn", "error". Case-insensitive.
//   - format: output format, one of "json" (production) or "console" (development).
func Init(level, format string) error {
	zapLevel, err := parseLevel(level)
	if err != nil {
		return fmt.Errorf("parse log level %q: %w", level, err)
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var encoder zapcore.Encoder
	switch strings.ToLower(format) {
	case "console":
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	default:
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	core := zapcore.NewCore(
		encoder,
		zapcore.AddSync(os.Stdout),
		zapLevel,
	)

	// AddCallerSkip(1) so callers of logger.L.Info() report their own location,
	// not the location inside the logger package.
	L = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	return nil
}

// Sync flushes any buffered log entries. Should be called before program exit.
func Sync() {
	if L != nil {
		_ = L.Sync()
	}
}

// parseLevel converts a string log level to zapcore.Level.
func parseLevel(s string) (zapcore.Level, error) {
	var level zapcore.Level
	err := level.UnmarshalText([]byte(strings.ToLower(s)))
	return level, err
}
