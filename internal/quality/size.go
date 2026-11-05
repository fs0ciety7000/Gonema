package quality

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Size units. Sizes follow the *arr convention: "MB" and "GB" are binary units
// (MiB, GiB), which is what download clients and file managers display.
const (
	MB = 1 << 20
	GB = 1 << 30
)

// SizeLimit bounds the size of a release, in MB per minute of runtime.
// MaxMBPerMin == 0 means no upper bound. Preferred is used for ranking only.
type SizeLimit struct {
	MinMBPerMin       float64 `json:"minMbPerMin"`
	PreferredMBPerMin float64 `json:"preferredMbPerMin"`
	MaxMBPerMin       float64 `json:"maxMbPerMin"`
}

// Bytes returns the minimum, preferred and maximum sizes in bytes for a runtime in
// minutes. max is 0 when there is no upper bound.
func (l SizeLimit) Bytes(runtimeMin int) (low, preferred, high int64) {
	m := float64(runtimeMin) * MB
	return int64(l.MinMBPerMin * m), int64(l.PreferredMBPerMin * m), int64(l.MaxMBPerMin * m)
}

// Validate checks that the limits are non-negative and ordered (min <= preferred <= max).
func (l SizeLimit) Validate() error {
	switch {
	case l.MinMBPerMin < 0 || l.PreferredMBPerMin < 0 || l.MaxMBPerMin < 0:
		return errors.New("size limits must not be negative")
	case l.PreferredMBPerMin < l.MinMBPerMin:
		return fmt.Errorf("preferred %.1f MB/min is below the minimum %.1f MB/min", l.PreferredMBPerMin, l.MinMBPerMin)
	case l.MaxMBPerMin > 0 && l.MaxMBPerMin < l.PreferredMBPerMin:
		return fmt.Errorf("maximum %.1f MB/min is below the preferred %.1f MB/min", l.MaxMBPerMin, l.PreferredMBPerMin)
	}
	return nil
}

// Sizes maps qualities to their size limits. A quality without an entry has no limits.
type Sizes map[ID]SizeLimit

// defaultSizes is read-only; DefaultSizes returns a copy. Values in MB per minute:
// a 120 min Bluray-1080p is accepted between 1.8 GB and 22.3 GB, preferred 8.2 GB.
var defaultSizes = Sizes{
	Unknown:     {0, 0, 0},
	Cam:         {0, 10, 50},
	Telesync:    {0, 10, 50},
	Telecine:    {0, 10, 50},
	Screener:    {0, 10, 50},
	Regional:    {0, 10, 50},
	SDTV:        {2, 8, 40},
	DVD:         {2, 10, 40},
	DVDR:        {15, 50, 80},
	WebRip480p:  {2, 8, 40},
	WebDL480p:   {2, 8, 40},
	Bluray480p:  {2, 10, 40},
	Bluray576p:  {2, 12, 50},
	HDTV720p:    {5, 15, 70},
	WebRip720p:  {5, 20, 80},
	WebDL720p:   {5, 20, 80},
	Bluray720p:  {8, 30, 110},
	HDTV1080p:   {10, 25, 120},
	WebRip1080p: {10, 40, 160},
	WebDL1080p:  {10, 40, 160},
	Bluray1080p: {15, 70, 190},
	Remux1080p:  {100, 220, 400},
	BRDisk1080p: {150, 300, 500},
	RawHD:       {50, 100, 300},
	HDTV2160p:   {20, 60, 250},
	WebRip2160p: {20, 100, 300},
	WebDL2160p:  {20, 100, 300},
	Bluray2160p: {40, 150, 400},
	Remux2160p:  {200, 450, 900},
	BRDisk2160p: {300, 650, 1100},
}

// DefaultSizes returns a fresh copy of the default size limits, safe to modify.
func DefaultSizes() Sizes { return maps.Clone(defaultSizes) }

// Limit returns the limits of id.
func (s Sizes) Limit(id ID) (SizeLimit, bool) {
	l, ok := s[id]
	return l, ok
}

// Validate checks every entry and reports all problems, in quality ID order.
func (s Sizes) Validate() error {
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(s)) {
		if !id.Valid() {
			errs = append(errs, fmt.Errorf("%w: size limits for unknown quality %q", ErrInvalid, id))
			continue
		}
		if err := s[id].Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%w: %s: %w", ErrInvalid, id.Name(), err))
		}
	}
	return errors.Join(errs...)
}
