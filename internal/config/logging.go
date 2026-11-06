package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
)

// Log output variables:
//
//	GONEMA_LOG_FILE         log file path, besides stderr; "" (default) = none.
//	                        A relative path is inside the data directory
//	                        ("logs/gonema.log").
//	GONEMA_LOG_MAX_SIZE_MB  size that rotates the file, 1-1024 MB; default 10.
//	GONEMA_LOG_KEEP         rotated files kept, 0-100; default 5.
//	GONEMA_LOG_BUFFER       entries kept in memory for the Logs screen,
//	                        100-100000; default 2000.
//
// Every output is redacted (API keys, tokens, passwords, Authorization
// headers): see package logbuf.
const (
	EnvLogFile      = "GONEMA_LOG_FILE"
	EnvLogMaxSizeMB = "GONEMA_LOG_MAX_SIZE_MB"
	EnvLogKeep      = "GONEMA_LOG_KEEP"
	EnvLogBuffer    = "GONEMA_LOG_BUFFER"
)

// LogOutput configures the log file and the in-memory buffer.
type LogOutput struct {
	File      string // absolute path, "" = no file
	MaxSizeMB int
	Keep      int
	Buffer    int
}

// DefaultLogOutput: no file, 10 MB, keep 5, 2000 entries in memory.
func DefaultLogOutput() LogOutput { return LogOutput{MaxSizeMB: 10, Keep: 5, Buffer: 2000} }

type logFlags struct{ file, size, keep, buffer *string }

func newLogFlags(fs *flag.FlagSet, str func(name, fallback string) string) *logFlags {
	d := DefaultLogOutput()
	return &logFlags{
		file: fs.String("log-file", str(EnvLogFile, ""),
			"log file besides stderr, rotated by size; relative paths are in the data directory ("+EnvLogFile+")"),
		size: fs.String("log-max-size", str(EnvLogMaxSizeMB, strconv.Itoa(d.MaxSizeMB)),
			"log file size that triggers a rotation, in MB, 1-1024 ("+EnvLogMaxSizeMB+")"),
		keep: fs.String("log-keep", str(EnvLogKeep, strconv.Itoa(d.Keep)),
			"rotated log files kept, 0-100 ("+EnvLogKeep+")"),
		buffer: fs.String("log-buffer", str(EnvLogBuffer, strconv.Itoa(d.Buffer)),
			"log entries kept in memory for the Logs screen, 100-100000 ("+EnvLogBuffer+")"),
	}
}

// finish validates the flags; dataDir resolves a relative file path.
func (f *logFlags) finish(dataDir string) (LogOutput, error) {
	var errs []error
	num := func(name, v string, lo, hi int) int {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < lo || n > hi {
			errs = append(errs, fmt.Errorf("config: %s %q: want a number from %d to %d", name, v, lo, hi))
		}
		return n
	}
	o := LogOutput{
		MaxSizeMB: num("log-max-size", *f.size, 1, 1024),
		Keep:      num("log-keep", *f.keep, 0, 100),
		Buffer:    num("log-buffer", *f.buffer, 100, 100000),
	}
	if p := strings.TrimSpace(*f.file); p != "" {
		if strings.ContainsRune(p, 0) {
			errs = append(errs, errors.New("config: log file: invalid path"))
		} else {
			if !filepath.IsAbs(p) && dataDir != "" {
				p = filepath.Join(dataDir, p)
			}
			o.File = filepath.Clean(p)
		}
	}
	return o, errors.Join(errs...)
}

func (o *LogOutput) logAttrs() []any {
	return []any{
		slog.String("log_file", o.File), slog.Int("log_max_size_mb", o.MaxSizeMB),
		slog.Int("log_keep", o.Keep), slog.Int("log_buffer", o.Buffer),
	}
}
