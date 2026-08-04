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

// Log acts as global zero-boilerplate logging handle (similar to Java @Slf4j log)
var Log = slog.Default()

type LineDelimiterWriter struct {
	Target io.Writer
}

// Write appends the missing newline byte character sequence to satisfy Vector stream parsers
func (w *LineDelimiterWriter) Write(p []byte) (n int, err error) {
	n, err = w.Target.Write(p)
	if err != nil {
		return n, err
	}
	_, _ = w.Target.Write([]byte("\n"))
	return n, nil
}

// InitStructuredLogger sets up network/console handlers and binds default runtime context
func InitStructuredLogger() {
	var logDestination io.Writer

	// Fetch vector pipeline endpoint from environment vars with loopback fallback
	vectorAddress := os.Getenv("LOGSTASH_TCP_DESTINATION")
	if vectorAddress == "" {
		vectorAddress = "127.0.0.1:6001"
	}

	// Attempt graceful TCP connection socket stream to Vector
	conn, err := net.DialTimeout("tcp", vectorAddress, 2*time.Second)
	if err != nil {
		// Fallback to standard stdout files if Vector boots late
		logDestination = os.Stdout
		slog.Warn("Vector socket unavailable, defaulting logging stream to console stdout", "error", err)
	} else {
		// MultiWriter splits log payloads concurrently to standard out and network socket
		networkWriter := &LineDelimiterWriter{Target: conn}
		logDestination = io.MultiWriter(os.Stdout, networkWriter)
	}

	// RUNTIME REFLECTION HANDLER
	handlerOpts := &slog.HandlerOptions{
		AddSource: true, // Forces slog to capture caller program counter coordinates
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {

			// 1. Intercept runtime source object block to isolate file path
			if a.Key == slog.SourceKey {
				source, ok := a.Value.Any().(*slog.Source)
				if !ok {
					return a
				}
				// Extract only current package directory and filename (e.g., pkg.routes)
				dir := filepath.Base(filepath.Dir(source.File))
				file := filepath.Base(source.File)
				cleanLoggerName := dir + "." + strings.TrimSuffix(file, ".go")

				return slog.Attr{Key: "logger_name", Value: slog.StringValue(cleanLoggerName)}
			}

			// 2. Standardize fields to match ClickHouse column types
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

	// Build parent JSON handler instance packing static service tokens
	baseLogger := slog.New(slog.NewJSONHandler(logDestination, handlerOpts))
	globalLogger := baseLogger.With(
		slog.String("service_name", "go-service"),
		slog.String("thread_name", "http-worker"),
	)

	// Bind to standard contexts so loose slog calls utilize specific setup
	slog.SetDefault(globalLogger)

	// Update package pointer handle to utilize finalized configured driver
	Log = globalLogger
}
