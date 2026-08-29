package logutils

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"
)

var Log *Logger

type Logger struct {
	level  LogLevel
	err    error
	fields map[string]any
	ctx    context.Context
}

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

func InitLogger(level string) {
	parsedLevel, err := parseLogLevel(level)
	if err != nil {
		log.Printf("Invalid log level '%s', defaulting to 'info'", level)
		parsedLevel = LevelInfo
	}
	Log = &Logger{level: parsedLevel}
	Log.Infof("Log level set to %v", parsedLevel)
}

func parseLogLevel(level string) (LogLevel, error) {
	switch strings.ToLower(level) {
	case "debug":
		return LevelDebug, nil
	case "info":
		return LevelInfo, nil
	case "warn":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	case "fatal":
		return LevelFatal, nil
	default:
		return LevelInfo, fmt.Errorf("invalid log level: %s", level)
	}
}

func (l *Logger) WithError(err error) *Logger {
	return &Logger{
		level:  l.level,
		err:    err,
		fields: l.fields,
		ctx:    l.ctx,
	}
}

func (l *Logger) WithField(key string, value any) *Logger {
	newFields := copyFields(l.fields)
	newFields[key] = value
	return &Logger{
		level:  l.level,
		err:    l.err,
		fields: newFields,
		ctx:    l.ctx,
	}
}

func (l *Logger) WithFields(fields map[string]any) *Logger {
	newFields := copyFields(l.fields)
	for k, v := range fields {
		newFields[k] = v
	}
	return &Logger{
		level:  l.level,
		err:    l.err,
		fields: newFields,
		ctx:    l.ctx,
	}
}

func (l *Logger) WithContext(ctx context.Context) *Logger {
	return &Logger{
		level:  l.level,
		err:    l.err,
		fields: l.fields,
		ctx:    ctx,
	}
}

func copyFields(fields map[string]any) map[string]any {
	if fields == nil {
		return make(map[string]any)
	}
	newFields := make(map[string]any, len(fields))
	for k, v := range fields {
		newFields[k] = v
	}
	return newFields
}

func (l *Logger) formatFields() string {
	if len(l.fields) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(" [")
	first := true
	for k, v := range l.fields {
		if !first {
			sb.WriteString(" ")
		}
		fmt.Fprintf(&sb, "%s=%v", k, v)
		first = false
	}
	sb.WriteString("]")
	return sb.String()
}

func (*Logger) traceInfo() string {
	_, file, line, ok := runtime.Caller(2)
	if ok {
		parts := strings.Split(file, "/")
		filename := parts[len(parts)-1]
		return fmt.Sprintf("%s:%d", filename, line)
	}
	return ""
}

func (l *Logger) shouldLog(level LogLevel) bool {
	return l.level <= level
}

func (l *Logger) logMessage(level LogLevel, message string, args ...any) {
	if !l.shouldLog(level) {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	trace := l.traceInfo()
	fields := l.formatFields()

	var formattedMessage string
	if len(args) > 0 {
		formattedMessage = fmt.Sprintf(message, args...)
	} else {
		formattedMessage = message
	}

	if l.err != nil {
		formattedMessage += fmt.Sprintf(": %v", l.err)
	}

	logMessage := fmt.Sprintf("[%s] %s %s%s %s",
		level.String(), timestamp, trace, fields, formattedMessage)

	switch level {
	case LevelDebug, LevelInfo:
		log.Print(logMessage)
	case LevelWarn:
		log.Print("WARNING: " + logMessage)
	case LevelError:
		log.Print("ERROR: " + logMessage)
	case LevelFatal:
		log.Print("FATAL: " + logMessage)
		os.Exit(1)
	}
}

func (l *Logger) Debugf(format string, args ...any) {
	l.logMessage(LevelDebug, format, args...)
}

func (l *Logger) Debug(message string) {
	l.logMessage(LevelDebug, "%s", message)
}

func (l *Logger) Infof(format string, args ...any) {
	l.logMessage(LevelInfo, format, args...)
}

func (l *Logger) Info(message string) {
	l.logMessage(LevelInfo, "%s", message)
}

func (l *Logger) Warnf(format string, args ...any) {
	l.logMessage(LevelWarn, format, args...)
}

func (l *Logger) Warn(message string) {
	l.logMessage(LevelWarn, "%s", message)
}

func (l *Logger) Errorf(format string, args ...any) {
	l.logMessage(LevelError, format, args...)
}

func (l *Logger) Error(message string) {
	l.logMessage(LevelError, "%s", message)
}

func (l *Logger) Fatalf(format string, args ...any) {
	l.logMessage(LevelFatal, format, args...)
}

func (l *Logger) Fatal(message string) {
	l.logMessage(LevelFatal, "%s", message)
}
