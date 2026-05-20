package logger

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// Level represents the severity of a log message
type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// String returns the string representation of the log level
func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger provides structured logging with levels
type Logger struct {
	minLevel   Level
	jsonFormat bool
	prefix     string
}

// Fields represents structured log fields
type Fields map[string]any

var defaultLogger = &Logger{
	minLevel:   InfoLevel,
	jsonFormat: false,
	prefix:     "",
}

// New creates a new logger with the given prefix
func New(prefix string) *Logger {
	return &Logger{
		minLevel:   defaultLogger.minLevel,
		jsonFormat: defaultLogger.jsonFormat,
		prefix:     prefix,
	}
}

// SetLevel sets the minimum log level for the default logger
func SetLevel(level Level) {
	defaultLogger.minLevel = level
}

// SetJSONFormat enables/disables JSON output for the default logger
func SetJSONFormat(enabled bool) {
	defaultLogger.jsonFormat = enabled
}

// SetLevel sets the minimum log level for this logger
func (l *Logger) SetLevel(level Level) {
	l.minLevel = level
}

// SetJSONFormat enables/disables JSON output for this logger
func (l *Logger) SetJSONFormat(enabled bool) {
	l.jsonFormat = enabled
}

// log is the internal logging function
func (l *Logger) log(level Level, message string, fields Fields) {
	if level < l.minLevel {
		return
	}

	if l.jsonFormat {
		l.logJSON(level, message, fields)
	} else {
		l.logText(level, message, fields)
	}
}

// logJSON outputs a JSON-formatted log entry
func (l *Logger) logJSON(level Level, message string, fields Fields) {
	entry := map[string]any{
		"timestamp": time.Now().Format(time.RFC3339),
		"level":     level.String(),
		"message":   message,
	}

	if l.prefix != "" {
		entry["component"] = l.prefix
	}

	for k, v := range fields {
		entry[k] = v
	}

	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("Failed to marshal log entry: %v", err)
		return
	}

	_, _ = fmt.Fprintln(os.Stdout, string(data))
}

// logText outputs a human-readable log entry
func (l *Logger) logText(level Level, message string, fields Fields) {
	// Build the log message
	var sb strings.Builder

	// Add level with color-like prefix
	switch level {
	case DebugLevel:
		sb.WriteString("🔍 [DEBUG] ")
	case InfoLevel:
		sb.WriteString("ℹ️  [INFO]  ")
	case WarnLevel:
		sb.WriteString("⚠️  [WARN]  ")
	case ErrorLevel:
		sb.WriteString("❌ [ERROR] ")
	}

	// Add component prefix if present
	if l.prefix != "" {
		sb.WriteString(fmt.Sprintf("[%s] ", l.prefix))
	}

	// Add message
	sb.WriteString(message)

	// Add fields
	if len(fields) > 0 {
		sb.WriteString(" | ")
		first := true
		for k, v := range fields {
			if !first {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s=%v", k, v))
			first = false
		}
	}

	log.Println(sb.String())
}

// Debug logs a debug message
func (l *Logger) Debug(message string, fields ...Fields) {
	f := mergeFields(fields...)
	l.log(DebugLevel, message, f)
}

// Info logs an info message
func (l *Logger) Info(message string, fields ...Fields) {
	f := mergeFields(fields...)
	l.log(InfoLevel, message, f)
}

// Warn logs a warning message
func (l *Logger) Warn(message string, fields ...Fields) {
	f := mergeFields(fields...)
	l.log(WarnLevel, message, f)
}

// Error logs an error message
func (l *Logger) Error(message string, fields ...Fields) {
	f := mergeFields(fields...)
	l.log(ErrorLevel, message, f)
}

// Debugf logs a formatted debug message
func (l *Logger) Debugf(format string, args ...any) {
	l.log(DebugLevel, fmt.Sprintf(format, args...), nil)
}

// Infof logs a formatted info message
func (l *Logger) Infof(format string, args ...any) {
	l.log(InfoLevel, fmt.Sprintf(format, args...), nil)
}

// Warnf logs a formatted warning message
func (l *Logger) Warnf(format string, args ...any) {
	l.log(WarnLevel, fmt.Sprintf(format, args...), nil)
}

// Errorf logs a formatted error message
func (l *Logger) Errorf(format string, args ...any) {
	l.log(ErrorLevel, fmt.Sprintf(format, args...), nil)
}

// Verbosef logs a formatted verbose/debug message (alias for Debugf)
func (l *Logger) Verbosef(format string, args ...any) {
	l.log(DebugLevel, fmt.Sprintf(format, args...), nil)
}

// Package-level convenience functions using the default logger

// Debug logs a debug message using the default logger
func Debug(message string, fields ...Fields) {
	defaultLogger.Debug(message, fields...)
}

// Info logs an info message using the default logger
func Info(message string, fields ...Fields) {
	defaultLogger.Info(message, fields...)
}

// Warn logs a warning message using the default logger
func Warn(message string, fields ...Fields) {
	defaultLogger.Warn(message, fields...)
}

// Error logs an error message using the default logger
func Error(message string, fields ...Fields) {
	defaultLogger.Error(message, fields...)
}

// Debugf logs a formatted debug message using the default logger
func Debugf(format string, args ...any) {
	defaultLogger.Debugf(format, args...)
}

// Infof logs a formatted info message using the default logger
func Infof(format string, args ...any) {
	defaultLogger.Infof(format, args...)
}

// Warnf logs a formatted warning message using the default logger
func Warnf(format string, args ...any) {
	defaultLogger.Warnf(format, args...)
}

// Errorf logs a formatted error message using the default logger
func Errorf(format string, args ...any) {
	defaultLogger.Errorf(format, args...)
}

// Verbosef logs a formatted verbose/debug message using the default logger (alias for Debugf)
func Verbosef(format string, args ...any) {
	defaultLogger.Verbosef(format, args...)
}

// mergeFields merges multiple Fields maps into one
func mergeFields(fields ...Fields) Fields {
	if len(fields) == 0 {
		return nil
	}

	result := make(Fields)
	for _, f := range fields {
		for k, v := range f {
			result[k] = v
		}
	}
	return result
}
