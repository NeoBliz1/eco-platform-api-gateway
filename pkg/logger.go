package pkg

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var Log = slog.Default()

func (w *LineDelimiterWriter) Write(p []byte) (n int, err error) {
	n, err = w.Target.Write(p)
	if err != nil {
		return n, err
	}
	_, _ = w.Target.Write([]byte("\n"))
	return n, nil
}

func InitStructuredLogger(cfg *Config) {
	var logDestination io.Writer

	vectorAddress := cfg.LogstashTcpDestination
	if vectorAddress == "" {
		vectorAddress = "127.0.0.1:6001"
	}

	conn, err := net.DialTimeout("tcp", vectorAddress, 2*time.Second)
	if err != nil {
		logDestination = os.Stdout
		slog.Warn("Vector socket unavailable, defaulting logging stream to console stdout", "error", err)
	} else {
		networkWriter := &LineDelimiterWriter{Target: conn}
		logDestination = io.MultiWriter(os.Stdout, networkWriter)
	}

	programLevel := new(slog.LevelVar) // Defaults to INFO (0)
	envLevel := strings.ToUpper(strings.TrimSpace(cfg.LogLevel))

	switch envLevel {
	case "DEBUG":
		programLevel.Set(slog.LevelDebug)
	case "INFO":
		programLevel.Set(slog.LevelInfo)
	case "WARN":
		programLevel.Set(slog.LevelWarn)
	case "ERROR":
		programLevel.Set(slog.LevelError)
	default:
		programLevel.Set(slog.LevelInfo)
	}

	handlerOpts := &slog.HandlerOptions{
		AddSource: true,
		Level:     programLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {

			if a.Key == slog.SourceKey {
				source, ok := a.Value.Any().(*slog.Source)
				if !ok {
					return a
				}
				dir := filepath.Base(filepath.Dir(source.File))
				file := filepath.Base(source.File)
				cleanLoggerName := dir + "." + strings.TrimSuffix(file, ".go")

				return slog.Attr{Key: "logger_name", Value: slog.StringValue(cleanLoggerName)}
			}

			if a.Key == slog.TimeKey {
				utcTime := a.Value.Time().UTC()
				return slog.Attr{Key: "timestamp", Value: slog.StringValue(utcTime.Format("2006-01-02 15:04:05.000"))}
			}
			if a.Key == slog.LevelKey {
				return slog.Attr{Key: "level", Value: slog.StringValue(a.Value.String())}
			}
			if a.Key == slog.MessageKey {
				return slog.Attr{Key: "message", Value: a.Value}
			}
			return a
		},
	}

	baseLogger := slog.New(slog.NewJSONHandler(logDestination, handlerOpts))
	globalLogger := baseLogger.With(
		slog.String("service_name", "go-service"),
		slog.String("thread_name", "http-worker"),
	)

	slog.SetDefault(globalLogger)

	Log = slog.Default()
}
