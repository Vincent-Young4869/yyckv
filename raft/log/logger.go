package log

import (
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"io"
	"log"
	"os"
	"sync"
)

// globalLogLevel holds the configured log level
var globalLogLevel zap.AtomicLevel

func InitLoggerLevel(loggingLevel string) {
	// Initialize globalLogLevel with a default level
	globalLogLevel = zap.NewAtomicLevel()
	switch loggingLevel {
	case "error":
		globalLogLevel.SetLevel(zap.ErrorLevel)
	case "warn":
		globalLogLevel.SetLevel(zap.WarnLevel)
	case "debug":
		globalLogLevel.SetLevel(zap.DebugLevel)
	case "info":
	default:
		globalLogLevel.SetLevel(zap.InfoLevel)
	}
}

func CreateZapLogger() *zap.Logger {
	config := zap.NewDevelopmentEncoderConfig()
	// Use a single space as the separator
	config.ConsoleSeparator = " "
	config.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")

	return zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(config),
		zapcore.AddSync(os.Stdout),
		globalLogLevel,
	))
}

type Logger interface {
	Debug(v ...interface{})
	Debugf(format string, v ...interface{})

	Error(v ...interface{})
	Errorf(format string, v ...interface{})

	Info(v ...interface{})
	Infof(format string, v ...interface{})

	Warning(v ...interface{})
	Warningf(format string, v ...interface{})

	Fatal(v ...interface{})
	Fatalf(format string, v ...interface{})

	Panic(v ...interface{})
	Panicf(format string, v ...interface{})
}

func SetLogger(l Logger) {
	raftLoggerMu.Lock()
	raftLogger = l
	raftLoggerMu.Unlock()
}

func ResetDefaultLogger() {
	SetLogger(defaultLogger)
}

func GetLogger() Logger {
	raftLoggerMu.Lock()
	defer raftLoggerMu.Unlock()
	return raftLogger
}

var (
	defaultLogger = &DefaultLogger{Logger: log.New(os.Stderr, "raft", log.LstdFlags)}
	discardLogger = &DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	raftLoggerMu  sync.Mutex
	raftLogger    = Logger(defaultLogger)
)

const (
	calldepth = 2
)

// DefaultLogger is a default implementation of the Logger interface.
type DefaultLogger struct {
	*log.Logger
	debug bool
}

func (l *DefaultLogger) EnableTimestamps() {
	l.SetFlags(l.Flags() | log.Ldate | log.Ltime)
}

func (l *DefaultLogger) EnableDebug() {
	l.debug = true
}

func (l *DefaultLogger) Debug(v ...interface{}) {
	if l.debug {
		l.Output(calldepth, header("DEBUG", fmt.Sprint(v...)))
	}
}

func (l *DefaultLogger) Debugf(format string, v ...interface{}) {
	if l.debug {
		l.Output(calldepth, header("DEBUG", fmt.Sprintf(format, v...)))
	}
}

func (l *DefaultLogger) Info(v ...interface{}) {
	l.Output(calldepth, header("INFO", fmt.Sprint(v...)))
}

func (l *DefaultLogger) Infof(format string, v ...interface{}) {
	l.Output(calldepth, header("INFO", fmt.Sprintf(format, v...)))
}

func (l *DefaultLogger) Error(v ...interface{}) {
	l.Output(calldepth, header("ERROR", fmt.Sprint(v...)))
}

func (l *DefaultLogger) Errorf(format string, v ...interface{}) {
	l.Output(calldepth, header("ERROR", fmt.Sprintf(format, v...)))
}

func (l *DefaultLogger) Warning(v ...interface{}) {
	l.Output(calldepth, header("WARN", fmt.Sprint(v...)))
}

func (l *DefaultLogger) Warningf(format string, v ...interface{}) {
	l.Output(calldepth, header("WARN", fmt.Sprintf(format, v...)))
}

func (l *DefaultLogger) Fatal(v ...interface{}) {
	l.Output(calldepth, header("FATAL", fmt.Sprint(v...)))
	os.Exit(1)
}

func (l *DefaultLogger) Fatalf(format string, v ...interface{}) {
	l.Output(calldepth, header("FATAL", fmt.Sprintf(format, v...)))
	os.Exit(1)
}

func (l *DefaultLogger) Panic(v ...interface{}) {
	l.Logger.Panic(v...)
}

func (l *DefaultLogger) Panicf(format string, v ...interface{}) {
	l.Logger.Panicf(format, v...)
}

func header(lvl, msg string) string {
	return fmt.Sprintf("%s: %s", lvl, msg)
}
