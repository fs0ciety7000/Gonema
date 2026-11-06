package config

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLogOutput(t *testing.T) {
	c, err := Load([]string{"-data-dir", "/srv/g", "-log-keep", "2"}, Env{Getenv: envOf(map[string]string{
		EnvLogFile: "logs/gonema.log", EnvLogMaxSizeMB: "50", EnvLogBuffer: "500",
	})}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := LogOutput{File: filepath.Join("/srv/g", "logs", "gonema.log"), MaxSizeMB: 50, Keep: 2, Buffer: 500}
	if c.Log != want {
		t.Errorf("log = %+v, want %+v", c.Log, want)
	}
	c, err = Load([]string{"-log-file", "/var/log/gonema.log"}, Env{}, io.Discard)
	if err != nil || c.Log.File != "/var/log/gonema.log" {
		t.Errorf("absolute = %+v %v", c.Log, err)
	}
	_, err = Load([]string{"-log-max-size", "0", "-log-keep", "101", "-log-buffer", "x"}, Env{}, io.Discard)
	for _, name := range []string{"log-max-size", "log-keep", "log-buffer"} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s: %v", name, err)
		}
	}
}
