package slog

import (
	"fmt"
	"log"
	"os"
)

type LogLevel int8

var syslogHost string
var syslogPort int = 0

var minLevel LogLevel
var logger *log.Logger

const (
	TRACE LogLevel = iota
	DEBUG
	INFO
	WARN
	ERROR
	FATAL
	PANIC
)

// Call Initialize after setting (or not setting) SyslogHost and SyslogPort when
// they're read from configuration source.
func Initialize() {
	logger = log.New(os.Stdout, "skynet", log.LstdFlags|log.Lshortfile)
}

func Panic(messages ...interface{}) {
	logger.Panic(fromMulti(messages))
}

func Panicf(format string, messages ...interface{}) {
	m := fmt.Sprintf(format, messages...)
	logger.Panic(m)
}

func Fatal(messages ...interface{}) {
	if minLevel <= FATAL {
		logger.Fatal(fromMulti(messages))
	}
}

func Fatalf(format string, messages ...interface{}) {
	if minLevel <= FATAL {
		m := fmt.Sprintf(format, messages...)
		logger.Fatal(m)
	}
}

func Error(messages ...interface{}) {
	if minLevel <= ERROR {
		logger.Println("[error]", fromMulti(messages))
	}
}

func Errorf(format string, messages ...interface{}) {
	if minLevel <= ERROR {
		m := fmt.Sprintf(format, messages...)
		logger.Println("[error]", m)
	}
}

func Warn(messages ...interface{}) {
	if minLevel <= WARN {
		logger.Println("[warning]", fromMulti(messages))
	}
}

func Warnf(format string, messages ...interface{}) {
	if minLevel <= WARN {
		m := fmt.Sprintf(format, messages...)
		logger.Println("[warning]", m)
	}
}

func Info(messages ...interface{}) {
	if minLevel <= INFO {
		logger.Println("[info]", fromMulti(messages))
	}
}

func Infof(format string, messages ...interface{}) {
	if minLevel <= INFO {
		m := fmt.Sprintf(format, messages...)
		logger.Println("[info]", m)
	}
}

func Debug(messages ...interface{}) {
	if minLevel <= DEBUG {
		logger.Println("[debug]", fromMulti(messages))
	}
}

func Debugf(format string, messages ...interface{}) {
	if minLevel <= DEBUG {
		m := fmt.Sprintf(format, messages...)
		logger.Println("[debug]", m)
	}
}

func Trace(messages ...interface{}) {
	if minLevel <= TRACE {
		logger.Println("[debug]", fromMulti(messages))
	}
}

func Tracef(format string, messages ...interface{}) {
	if minLevel <= TRACE {
		m := fmt.Sprintf(format, messages...)
		logger.Println("[debug]", m)
	}
}

func Println(level LogLevel, messages ...interface{}) {

	switch level {
	case DEBUG:
		Debugf("%v", messages)
	case TRACE:
		Tracef("%v", messages)
	case INFO:
		Infof("%v", messages)
	case WARN:
		Warnf("%v", messages)
	case ERROR:
		Errorf("%v", messages)
	case FATAL:
		Fatalf("%v", messages)
	case PANIC:
		Panicf("%v", messages)
	}

	return
}

func Printf(level LogLevel, format string, messages ...interface{}) {

	switch level {
	case DEBUG:
		Debugf(format, messages)
	case TRACE:
		Tracef(format, messages)
	case INFO:
		Infof(format, messages)
	case WARN:
		Warnf(format, messages)
	case ERROR:
		Errorf(format, messages)
	case FATAL:
		Fatalf(format, messages)
	case PANIC:
		Panicf(format, messages)
	}

	return
}

func SetSyslogHost(host string) {
	syslogHost = host
}

func SetSyslogPort(port int) {
	syslogPort = port
}

func SetLogLevel(level LogLevel) {
	minLevel = level
}

func GetLogLevel() LogLevel {
	return minLevel
}

func fromMulti(messages ...interface{}) string {
	var r string
	for x := 0; x < len(messages); x++ {
		r = r + messages[x].(string)
		if x < len(messages) {
			r = r + "  "
		}
	}
	return r
}

func LevelFromString(l string) (level LogLevel) {
	switch l {
	case "DEBUG":
		level = DEBUG
	case "TRACE":
		level = TRACE
	case "INFO":
		level = INFO
	case "WARN":
		level = WARN
	case "ERROR":
		level = ERROR
	case "FATAL":
		level = FATAL
	case "PANIC":
		level = PANIC
	}

	return
}
