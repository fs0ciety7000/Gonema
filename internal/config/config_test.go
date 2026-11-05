package config

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad(t *testing.T) {
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		container bool
		want      Config
	}{
		{
			name: "defaults",
			want: Config{DataDir: abs("./data"), Bind: "127.0.0.1:7676", LogLevel: slog.LevelInfo, LogFormat: "text", Workers: 4},
		},
		{
			name:      "container defaults",
			container: true,
			want:      Config{DataDir: "/config", Bind: "0.0.0.0:7676", LogLevel: slog.LevelInfo, LogFormat: "text", Workers: 4},
		},
		{
			name: "environment",
			env: map[string]string{
				EnvDataDir: "/srv/gonema", EnvBind: "[::1]:9000", EnvLogLevel: "DEBUG",
				EnvLogFormat: "JSON", EnvBaseURL: "/gonema/", EnvWorkers: "8",
			},
			want: Config{DataDir: "/srv/gonema", Bind: "[::1]:9000", LogLevel: slog.LevelDebug, LogFormat: "json", BaseURL: "/gonema", Workers: 8},
		},
		{
			name: "flags win over environment",
			args: []string{"-bind", "127.0.0.1:0", "-log-level", "warn", "-base-url", "apps/gonema"},
			env:  map[string]string{EnvBind: "0.0.0.0:1", EnvLogLevel: "debug", EnvDataDir: "/d"},
			want: Config{DataDir: "/d", Bind: "127.0.0.1:0", LogLevel: slog.LevelWarn, LogFormat: "text", BaseURL: "/apps/gonema", Workers: 4},
		},
		{
			name: "root base URL is empty",
			args: []string{"-base-url", "/"},
			want: Config{DataDir: abs("./data"), Bind: "127.0.0.1:7676", LogLevel: slog.LevelInfo, LogFormat: "text", Workers: 4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(tt.args, Env{Getenv: envOf(tt.env), InContainer: tt.container}, io.Discard)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("config (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string // substring of the error
	}{
		{"bad bind", []string{"-bind", "7676"}, nil, "want host:port"},
		{"bind hostname", []string{"-bind", "example.com:80"}, nil, "IP address or localhost"},
		{"bad port", []string{"-bind", "127.0.0.1:99999"}, nil, "invalid port"},
		{"bad level", nil, map[string]string{EnvLogLevel: "loud"}, "log level"},
		{"bad format", []string{"-log-format", "xml"}, nil, "log format"},
		{"base URL traversal", []string{"-base-url", "/a/../b"}, nil, "'..'"},
		{"base URL chars", []string{"-base-url", "/a?b"}, nil, "only letters"},
		{"workers", nil, map[string]string{EnvWorkers: "0"}, "workers"},
		{"extra argument", []string{"oops"}, nil, "unexpected argument"},
		{"unknown flag", []string{"-nope"}, nil, "not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.args, Env{Getenv: envOf(tt.env)}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestLoadHelp(t *testing.T) {
	var out strings.Builder
	_, err := Load([]string{"-h"}, Env{}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(out.String(), EnvBind) {
		t.Errorf("usage does not mention %s:\n%s", EnvBind, out.String())
	}
}

func TestIsLoopback(t *testing.T) {
	for bind, want := range map[string]bool{
		"127.0.0.1:7676": true, "[::1]:7676": true, "localhost:1": true,
		"0.0.0.0:7676": false, ":7676": false, "192.168.1.2:7676": false,
	} {
		c := Config{Bind: bind}
		if got := c.IsLoopback(); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", bind, got, want)
		}
	}
}
