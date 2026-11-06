package quality_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fs0ciety7000/gonema/internal/quality"
)

func TestSizeLimit_Bytes(t *testing.T) {
	t.Parallel()
	l := quality.SizeLimit{MinMBPerMin: 10, PreferredMBPerMin: 20, MaxMBPerMin: 0}
	low, pref, high := l.Bytes(100)
	if low != 1000*quality.MB || pref != 2000*quality.MB || high != 0 {
		t.Errorf("Bytes(100) = %d, %d, %d", low, pref, high)
	}
}

func TestSizeLimit_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		limit   quality.SizeLimit
		wantErr string
	}{
		{"ok", quality.SizeLimit{MinMBPerMin: 1, PreferredMBPerMin: 2, MaxMBPerMin: 3}, ""},
		{"no max", quality.SizeLimit{MinMBPerMin: 1, PreferredMBPerMin: 2}, ""},
		{"negative", quality.SizeLimit{MinMBPerMin: -1}, "must not be negative"},
		{"preferred below min", quality.SizeLimit{MinMBPerMin: 5, PreferredMBPerMin: 2}, "preferred 2.0 MB/min is below the minimum 5.0 MB/min"},
		{"max below preferred", quality.SizeLimit{MinMBPerMin: 1, PreferredMBPerMin: 9, MaxMBPerMin: 3}, "maximum 3.0 MB/min is below the preferred 9.0 MB/min"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.limit.Validate()
			checkErr(t, err, tt.wantErr)
		})
	}
}

func TestDefaultSizes(t *testing.T) {
	t.Parallel()
	s := quality.DefaultSizes()
	if err := s.Validate(); err != nil {
		t.Fatalf("default sizes are invalid: %v", err)
	}
	for _, q := range quality.All() {
		if _, ok := s.Limit(q.ID); !ok {
			t.Errorf("no default size for %s", q.ID)
		}
	}
	s[quality.Bluray1080p] = quality.SizeLimit{}
	if l, _ := quality.DefaultSizes().Limit(quality.Bluray1080p); l.MaxMBPerMin == 0 {
		t.Error("DefaultSizes returned shared state")
	}
}

func TestSizes_Validate(t *testing.T) {
	t.Parallel()
	s := quality.Sizes{
		"bogus":             {},
		quality.Bluray720p:  {MinMBPerMin: -1},
		quality.Bluray1080p: {MinMBPerMin: 1, PreferredMBPerMin: 2},
	}
	err := s.Validate()
	if !errors.Is(err, quality.ErrInvalid) {
		t.Fatalf("Validate = %v, want ErrInvalid", err)
	}
	for _, want := range []string{`unknown quality "bogus"`, "Bluray-720p: size limits must not be negative"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// checkErr fails unless err is nil when want is empty, or contains want.
func checkErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("unexpected error: %v", err)
	case want != "" && err == nil:
		t.Errorf("no error, want one containing %q", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Errorf("error %q does not contain %q", err, want)
	}
}
