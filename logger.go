package kcp

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/pkg/errors"
)

type Logger struct {
	l *log.Logger

	// Should not use isDiscard in logger.go
	// Not need atomic for that
	isEnable bool
}

func New(out io.Writer, prefix string, flag int) *Logger {
	_l := log.New(out, prefix, flag)
	l := &Logger{l: _l, isEnable: true}

	return l
}

func (l *Logger) Disable() {
	l.isEnable = false
}

func (l *Logger) IsEnable() bool {
	return l.isEnable
}

var (
	DebugLogger   *Logger
	InfoLogger    *Logger
	WarningLogger *Logger
	ErrorLogger   *Logger
	// Only used on test.
	TestLogger      *Logger
	TestFatalLogger *Logger
)

const (
	ErrorLevelLog int = 3
	WarnLevelLog  int = 2
	InfoLevelLog  int = 1
	DebugLevelLog int = 0
)

func LoggerDefault() error {
	return LoggerInit(nil, DebugLevelLog)
}

func LoggerInit(outputFile *string, logLevel int) error {
	var file *os.File
	var err error
	if outputFile != nil {
		file, err = os.OpenFile(*outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			return err
		}
	}

	if logLevel > 3 || logLevel < 0 {
		return errors.New("invalid log level. must in [0,3]")
	}

	outIo := func(_file *os.File) io.Writer {
		if _file != nil {
			return _file
		}
		return os.Stdout
	}

	TestLogger = New(outIo(file), "[TEST] ", log.Ldate|log.Ltime|log.Lshortfile)
	TestFatalLogger = New(outIo(file), "[TEST Fatal] ", log.Ldate|log.Ltime|log.Lshortfile)

	DebugLogger = New(outIo(file), "[DEBUG] ", log.Ldate|log.Ltime|log.Lshortfile)
	InfoLogger = New(outIo(file), "[INFO] ", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLogger = New(outIo(file), "[WARN] ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = New(outIo(file), "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile)

	switch logLevel {
	// case 0: do nothing
	case 3:
		WarningLogger.Disable()
		fallthrough
	case 2:
		InfoLogger.Disable()
		fallthrough
	case 1:
		DebugLogger.Disable()
	}

	return nil
}

func LogTest(format string, v ...any) {
	TestLogger.l.Output(2, fmt.Sprintf(format, v...))
}

func LogTestFatalf(format string, v ...any) {
	TestFatalLogger.l.Output(2, fmt.Sprintf(format, v...))
	os.Exit(1)
}

func LogDebug(format string, v ...any) {
	if DebugLogger.IsEnable() {
		DebugLogger.l.Output(2, fmt.Sprintf(format, v...))
	}
}

func LogInfo(format string, v ...any) {
	if InfoLogger.IsEnable() {
		InfoLogger.l.Output(2, fmt.Sprintf(format, v...))
	}
}

func LogWarn(format string, v ...any) {
	if WarningLogger.IsEnable() {
		WarningLogger.l.Output(2, fmt.Sprintf(format, v...))
	}
}

func LogError(format string, v ...any) {
	if ErrorLogger.IsEnable() {
		ErrorLogger.l.Output(2, fmt.Sprintf(format, v...))
	}
}

func LogFatalf(format string, v ...any) {
	ErrorLogger.l.Output(2, fmt.Sprintf(format, v...))
	os.Exit(1)
}
