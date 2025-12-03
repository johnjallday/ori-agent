package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// LogLevel represents the severity of a log message
type LogLevel string

const (
	Debug LogLevel = "Debug"
	Info  LogLevel = "Info"
	Warn  LogLevel = "Warn"
	Error LogLevel = "Error"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run migrate-logging.go <file>")
		os.Exit(1)
	}

	filename := os.Args[1]
	if err := migrateFile(filename); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func migrateFile(filename string) error {
	// Read file
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	modified := false
	needsLoggerImport := false

	// Process each line
	for i, line := range lines {
		if strings.Contains(line, "log.Printf") {
			newLine, changed := convertLogPrintf(line)
			if changed {
				lines[i] = newLine
				modified = true
				needsLoggerImport = true
			}
		}
	}

	if !modified {
		fmt.Printf("No changes needed in %s\n", filename)
		return nil
	}

	// Update imports if needed
	if needsLoggerImport {
		lines = updateImports(lines)
	}

	// Write back
	output := strings.Join(lines, "\n")
	if err := os.WriteFile(filename, []byte(output), 0644); err != nil {
		return err
	}

	fmt.Printf("✅ Migrated %s\n", filename)
	return nil
}

func convertLogPrintf(line string) (string, bool) {
	// Match log.Printf patterns
	re := regexp.MustCompile(`log\.Printf\((.*)\)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) < 2 {
		return line, false
	}

	args := matches[1]

	// Extract format string and arguments
	formatStr, argsList := extractFormatAndArgs(args)
	if formatStr == "" {
		return line, false
	}

	// Determine log level
	level := determineLogLevel(formatStr)

	// Clean up message (remove emojis, prefixes)
	message := cleanMessage(formatStr)

	// Extract field names and values
	fields := extractFields(formatStr, argsList)

	// Build new log statement
	indent := strings.Repeat("\t", len(line)-len(strings.TrimLeft(line, "\t")))
	newLine := buildLogStatement(indent, level, message, fields)

	return newLine, true
}

func extractFormatAndArgs(args string) (string, []string) {
	// Simple parser for "format", arg1, arg2, ...
	parts := []string{}
	current := ""
	inQuotes := false
	parenDepth := 0

	for _, ch := range args {
		switch ch {
		case '"':
			if inQuotes && len(current) > 0 && current[len(current)-1] != '\\' {
				inQuotes = false
			} else if !inQuotes {
				inQuotes = true
			}
			current += string(ch)
		case ',':
			if !inQuotes && parenDepth == 0 {
				parts = append(parts, strings.TrimSpace(current))
				current = ""
			} else {
				current += string(ch)
			}
		case '(', '{', '[':
			parenDepth++
			current += string(ch)
		case ')', '}', ']':
			parenDepth--
			current += string(ch)
		default:
			current += string(ch)
		}
	}
	if current != "" {
		parts = append(parts, strings.TrimSpace(current))
	}

	if len(parts) == 0 {
		return "", nil
	}

	// First part is format string
	formatStr := strings.Trim(parts[0], `"`)
	return formatStr, parts[1:]
}

func determineLogLevel(message string) LogLevel {
	lower := strings.ToLower(message)

	// Error indicators
	if strings.Contains(lower, "error") ||
		strings.Contains(message, "❌") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "fail:") {
		return Error
	}

	// Warning indicators
	if strings.Contains(lower, "warning") ||
		strings.Contains(message, "⚠️") ||
		strings.HasPrefix(lower, "warn") {
		return Warn
	}

	// Info indicators
	if strings.Contains(message, "✅") ||
		strings.Contains(message, "🎯") ||
		strings.Contains(message, "📊") ||
		strings.Contains(lower, "success") ||
		strings.Contains(lower, "completed") ||
		strings.Contains(lower, "created") {
		return Info
	}

	// Default to debug
	return Debug
}

func cleanMessage(formatStr string) string {
	// Remove emojis
	emojis := []string{"❌", "✅", "⚠️", "🎯", "📊", "🔧", "📤", "📥"}
	msg := formatStr
	for _, emoji := range emojis {
		msg = strings.ReplaceAll(msg, emoji, "")
	}

	// Remove common prefixes
	prefixes := []string{"Error:", "Warning:", "DEBUG:", "Info:"}
	for _, prefix := range prefixes {
		msg = strings.TrimPrefix(msg, prefix)
		msg = strings.TrimPrefix(msg, strings.ToLower(prefix))
	}

	msg = strings.TrimSpace(msg)

	// Remove format specifiers for message
	msg = regexp.MustCompile(`%[vsdqfgTx+#-]*`).ReplaceAllString(msg, "")
	msg = strings.TrimSpace(msg)

	// Remove trailing colons and extra spaces
	msg = strings.TrimSuffix(msg, ":")
	msg = regexp.MustCompile(`\s+`).ReplaceAllString(msg, " ")
	msg = strings.TrimSpace(msg)

	return msg
}

func extractFields(formatStr string, args []string) map[string]string {
	fields := make(map[string]string)

	// Common field name mapping based on context
	fieldNames := inferFieldNames(formatStr, args)

	for i, arg := range args {
		if i < len(fieldNames) {
			fields[fieldNames[i]] = arg
		} else {
			fields[fmt.Sprintf("arg%d", i+1)] = arg
		}
	}

	return fields
}

func inferFieldNames(formatStr string, args []string) []string {
	lower := strings.ToLower(formatStr)
	names := make([]string, len(args))

	// Common patterns
	keywords := map[string]string{
		"error":     "error",
		"err":       "error",
		"workspace": "workspace_id",
		"studio":    "workspace_id",
		"agent":     "agent",
		"name":      "name",
		"task":      "task_id",
		"plugin":    "plugin",
		"tool":      "tool",
		"server":    "server",
		"model":     "model",
		"duration":  "duration",
		"time":      "duration",
		"count":     "count",
		"message":   "message",
		"result":    "result",
		"response":  "response",
		"status":    "status",
		"tokens":    "tokens",
		"cost":      "cost",
	}

	// Try to infer from format string
	for keyword, fieldName := range keywords {
		if strings.Contains(lower, keyword) {
			if len(names) > 0 && names[0] == "" {
				names[0] = fieldName
				break
			}
		}
	}

	// Check variable names in args
	for i, arg := range args {
		arg = strings.TrimSpace(arg)
		if names[i] == "" {
			// Use the variable name if it's a simple identifier
			if regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`).MatchString(arg) {
				names[i] = arg
			} else if strings.HasPrefix(arg, "time.Since(") {
				names[i] = "duration"
			} else if strings.Contains(arg, ".") {
				// Extract last part of dot notation
				parts := strings.Split(arg, ".")
				names[i] = strings.ToLower(parts[len(parts)-1])
			} else {
				names[i] = fmt.Sprintf("value%d", i+1)
			}
		}
	}

	return names
}

func buildLogStatement(indent string, level LogLevel, message string, fields map[string]string) string {
	if len(fields) == 0 {
		return fmt.Sprintf("%slogger.%s(%q, logger.Fields{})", indent, level, message)
	}

	// Build fields map
	fieldStr := "logger.Fields{"
	first := true
	for key, value := range fields {
		if !first {
			fieldStr += ", "
		}
		fieldStr += fmt.Sprintf("%q: %s", key, value)
		first = false
	}
	fieldStr += "}"

	return fmt.Sprintf("%slogger.%s(%q, %s)", indent, level, message, fieldStr)
}

func updateImports(lines []string) []string {
	hasLogImport := false
	hasLoggerImport := false
	logImportLine := -1
	importBlockStart := -1
	importBlockEnd := -1

	// Find imports
	inImportBlock := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "import (") {
			inImportBlock = true
			importBlockStart = i
			continue
		}

		if inImportBlock && trimmed == ")" {
			importBlockEnd = i
			inImportBlock = false
			continue
		}

		if inImportBlock || strings.HasPrefix(trimmed, "import ") {
			if strings.Contains(line, `"log"`) && !strings.Contains(line, "logger") {
				hasLogImport = true
				logImportLine = i
			}
			if strings.Contains(line, `"github.com/johnjallday/ori-agent/internal/logger"`) {
				hasLoggerImport = true
			}
		}
	}

	// Remove "log" import if it exists
	if hasLogImport && logImportLine >= 0 {
		lines[logImportLine] = "" // Remove the line
	}

	// Add logger import if not present
	if !hasLoggerImport && importBlockEnd > importBlockStart {
		// Add logger import before the closing )
		loggerImport := "\t\"github.com/johnjallday/ori-agent/internal/logger\""
		lines = append(lines[:importBlockEnd], append([]string{loggerImport}, lines[importBlockEnd:]...)...)
	}

	return lines
}
