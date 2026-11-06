// Package config reads the server configuration from command-line flags and
// environment variables (flags win), applies defaults and validates it.
//
// Variables:
//
//	GONEMA_DATA_DIR    data directory (database, master key, image cache).
//	                   Default "/config" in a container, "./data" otherwise.
//	GONEMA_BIND        listen address, default "127.0.0.1:7676" ("0.0.0.0:7676"
//	                   in a container, where publishing the port is explicit).
//	GONEMA_LOG_LEVEL   debug | info | warn | error, default info.
//	GONEMA_LOG_FORMAT  text | json, default text.
//	GONEMA_BASE_URL    URL path prefix when served behind a reverse proxy under a
//	                   sub-path ("/gonema"); default "" (served at the root).
//	GONEMA_WORKERS     number of background job workers, default 4.
//
// The security variables (GONEMA_AUTH, GONEMA_COOKIE_SECURE,
// GONEMA_TRUSTED_ORIGINS, GONEMA_OUTBOUND_*, GONEMA_WEBHOOK_ALLOW_PRIVATE) are
// described in security.go and docs/security.md.
//
// The default port 7676 avoids the ports of other well-known self-hosted media
// apps (7878, 8989, 8686, 8787, 9696, 6767, 6969, 5055, 8096, 32400, 8080,
// 9091, 8112, 6789, 8181).
//
// The master key (GONEMA_MASTER_KEY) is read by package secretbox, not here, so
// the configuration never holds a secret and can be logged as a whole.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultPort is the default TCP port.
const DefaultPort = 7676

// Environment variable names.
const (
	EnvDataDir   = "GONEMA_DATA_DIR"
	EnvBind      = "GONEMA_BIND"
	EnvLogLevel  = "GONEMA_LOG_LEVEL"
	EnvLogFormat = "GONEMA_LOG_FORMAT"
	EnvBaseURL   = "GONEMA_BASE_URL"
	EnvWorkers   = "GONEMA_WORKERS"
)

// Log formats.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// Config is the effective server configuration. It holds no secret.
type Config struct {
	DataDir   string     // absolute, cleaned
	Bind      string     // host:port
	LogLevel  slog.Level //
	LogFormat string     // FormatText or FormatJSON
	BaseURL   string     // "" or "/path" without trailing slash
	Workers   int        // background job workers
	Security  Security   // authentication and outbound policy
	Log       LogOutput  // log file and in-memory buffer (logging.go)
}

// Env abstracts the process environment for tests.
type Env struct {
	Getenv      func(string) string
	InContainer bool // running in Docker/Podman: changes DataDir and Bind defaults
}

// Defaults returns the defaults for the environment.
func Defaults(inContainer bool) Config {
	c := Config{
		DataDir:   "./data",
		Bind:      net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultPort)),
		LogLevel:  slog.LevelInfo,
		LogFormat: FormatText,
		Workers:   4,
		Security:  DefaultSecurity(),
		Log:       DefaultLogOutput(),
	}
	if inContainer {
		c.DataDir = "/config"
		c.Bind = net.JoinHostPort("0.0.0.0", strconv.Itoa(DefaultPort))
	}
	return c
}

// Load builds the configuration from the environment then args (flags without
// the program and subcommand names). Errors name the offending flag or variable.
// flag.ErrHelp is returned when -h was given (usage was written to out).
func Load(args []string, env Env, out io.Writer) (Config, error) {
	getenv := env.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	def := Defaults(env.InContainer)
	str := func(name, fallback string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return fallback
	}

	fs := flag.NewFlagSet("gonema serve", flag.ContinueOnError)
	fs.SetOutput(out)
	dataDir := fs.String("data-dir", str(EnvDataDir, def.DataDir), "data directory ("+EnvDataDir+")")
	bind := fs.String("bind", str(EnvBind, def.Bind), "listen address host:port ("+EnvBind+")")
	level := fs.String("log-level", str(EnvLogLevel, "info"), "log level: debug, info, warn, error ("+EnvLogLevel+")")
	format := fs.String("log-format", str(EnvLogFormat, def.LogFormat), "log format: text or json ("+EnvLogFormat+")")
	baseURL := fs.String("base-url", str(EnvBaseURL, ""), "URL path prefix behind a reverse proxy, e.g. /gonema ("+EnvBaseURL+")")
	workers := fs.String("workers", str(EnvWorkers, strconv.Itoa(def.Workers)), "background job workers, 1-32 ("+EnvWorkers+")")
	sec := newSecurityFlags(fs, str)
	logOut := newLogFlags(fs, str)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("config: unexpected argument %q", fs.Arg(0))
	}

	var errs []error
	c := Config{LogFormat: strings.ToLower(*format)}

	if d, err := cleanDataDir(*dataDir); err != nil {
		errs = append(errs, err)
	} else {
		c.DataDir = d
	}
	if err := validateBind(*bind); err != nil {
		errs = append(errs, err)
	}
	c.Bind = *bind
	if err := c.LogLevel.UnmarshalText([]byte(*level)); err != nil {
		errs = append(errs, fmt.Errorf("config: log level %q: want debug, info, warn or error", *level))
	}
	if c.LogFormat != FormatText && c.LogFormat != FormatJSON {
		errs = append(errs, fmt.Errorf("config: log format %q: want text or json", *format))
	}
	if b, err := cleanBaseURL(*baseURL); err != nil {
		errs = append(errs, err)
	} else {
		c.BaseURL = b
	}
	if n, err := strconv.Atoi(*workers); err != nil || n < 1 || n > 32 {
		errs = append(errs, fmt.Errorf("config: workers %q: want a number from 1 to 32", *workers))
	} else {
		c.Workers = n
	}
	var err error
	if c.Security, err = sec.finish(); err != nil {
		errs = append(errs, err)
	}
	if c.Log, err = logOut.finish(c.DataDir); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

func cleanDataDir(d string) (string, error) {
	if strings.TrimSpace(d) == "" {
		return "", errors.New("config: data directory is empty")
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return "", fmt.Errorf("config: data directory %q: %w", d, err)
	}
	return abs, nil
}

func validateBind(b string) error {
	host, port, err := net.SplitHostPort(b)
	if err != nil {
		return fmt.Errorf("config: bind address %q: want host:port, e.g. 127.0.0.1:%d", b, DefaultPort)
	}
	if host != "" && host != "localhost" && net.ParseIP(host) == nil {
		return fmt.Errorf("config: bind address %q: host must be an IP address or localhost", b)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 0 || p > 65535 {
		return fmt.Errorf("config: bind address %q: invalid port", b)
	}
	return nil
}

// cleanBaseURL normalises a reverse-proxy sub-path: "" or "/" give "",
// "gonema/" gives "/gonema". Only letters, digits, '-', '_', '.' and '/' are
// accepted, without empty, "." or ".." segments.
func cleanBaseURL(b string) (string, error) {
	b = strings.Trim(strings.TrimSpace(b), "/")
	if b == "" {
		return "", nil
	}
	for seg := range strings.SplitSeq(b, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("config: base URL %q: empty, '.' or '..' path segment", b)
		}
		for _, r := range seg {
			ok := r == '-' || r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !ok {
				return "", fmt.Errorf("config: base URL %q: only letters, digits, '-', '_', '.' and '/' are allowed", b)
			}
		}
	}
	return "/" + b, nil
}

// LogAttrs returns the configuration as log attributes (it holds no secret).
func (c *Config) LogAttrs() []any {
	return append([]any{
		slog.String("data_dir", c.DataDir),
		slog.String("bind", c.Bind),
		slog.String("base_url", c.BaseURL),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("log_format", c.LogFormat),
		slog.Int("workers", c.Workers),
	}, append(c.Security.logAttrs(), c.Log.logAttrs()...)...)
}

// IsLoopback reports whether Bind only accepts local connections.
func (c *Config) IsLoopback() bool {
	host, _, err := net.SplitHostPort(c.Bind)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewLogger returns the slog logger described by the configuration.
func (c *Config) NewLogger(w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.LogFormat == FormatJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
