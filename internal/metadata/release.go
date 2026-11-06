package metadata

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// ReleaseType is the kind of a dated release, numbered as TMDB does.
type ReleaseType int

// Release types.
const (
	ReleasePremiere          ReleaseType = 1
	ReleaseTheatricalLimited ReleaseType = 2
	ReleaseTheatrical        ReleaseType = 3
	ReleaseDigital           ReleaseType = 4
	ReleasePhysical          ReleaseType = 5
	ReleaseTV                ReleaseType = 6
)

var releaseTypeNames = [...]string{"", "premiere", "limited theatrical", "theatrical", "digital", "physical", "TV"}

// String returns the text form of t.
func (t ReleaseType) String() string {
	if t > 0 && int(t) < len(releaseTypeNames) {
		return releaseTypeNames[t]
	}
	return fmt.Sprintf("ReleaseType(%d)", int(t))
}

// ReleaseDate is one dated release in a region.
type ReleaseDate struct {
	Type          ReleaseType `json:"type"`
	Date          Date        `json:"date"`
	Certification string      `json:"certification,omitempty"`
	Language      string      `json:"language,omitempty"`
	Note          string      `json:"note,omitempty"`
}

// ReleaseDates are the releases of a movie in one region (ISO 3166-1 code).
type ReleaseDates struct {
	Region string        `json:"region"`
	Dates  []ReleaseDate `json:"dates"`
}

// earliest returns the earliest known date of type t in the region.
func (r *ReleaseDates) earliest(t ReleaseType) Date {
	var best Date
	for i := range r.Dates {
		d := &r.Dates[i]
		if d.Type == t && !d.Date.IsZero() && (best.IsZero() || d.Date.Before(best)) {
			best = d.Date
		}
	}
	return best
}

// Cinema returns the cinema release of the region: the earliest theatrical (3)
// date, else the earliest limited theatrical (2), else the earliest premiere (1).
func (r *ReleaseDates) Cinema() (Date, ReleaseType) {
	for _, t := range [...]ReleaseType{ReleaseTheatrical, ReleaseTheatricalLimited, ReleasePremiere} {
		if d := r.earliest(t); !d.IsZero() {
			return d, t
		}
	}
	return Date{}, 0
}

// Digital returns the earliest digital (4) release of the region.
func (r *ReleaseDates) Digital() Date { return r.earliest(ReleaseDigital) }

// Physical returns the earliest physical (5) release of the region.
func (r *ReleaseDates) Physical() Date { return r.earliest(ReleasePhysical) }

// Certification returns the certification of the region, taken from the
// theatrical releases first, then any other release type.
func (r *ReleaseDates) Certification() string {
	for _, t := range [...]ReleaseType{ReleaseTheatrical, ReleaseTheatricalLimited, ReleasePremiere, ReleaseDigital, ReleasePhysical, ReleaseTV} {
		for i := range r.Dates {
			if r.Dates[i].Type == t {
				if c := strings.TrimSpace(r.Dates[i].Certification); c != "" {
					return c
				}
			}
		}
	}
	return ""
}

// DatedRelease is a chosen date with where it comes from.
type DatedRelease struct {
	Date   Date        `json:"date,omitzero"`
	Region string      `json:"region,omitempty"`
	Type   ReleaseType `json:"type,omitempty"`
}

// ReleaseSelection holds the dates stored in movies.release_cinema,
// release_digital, release_physical and the certification.
type ReleaseSelection struct {
	Cinema              DatedRelease `json:"cinema"`
	Digital             DatedRelease `json:"digital"`
	Physical            DatedRelease `json:"physical"`
	Certification       string       `json:"certification,omitempty"`
	CertificationRegion string       `json:"certificationRegion,omitempty"`
}

// FallbackRegion is used when the configured region has no date of a kind.
const FallbackRegion = "US"

// SelectReleases chooses the cinema, digital and physical dates and the
// certification of a movie.
//
// Each kind is chosen independently: from the configured region if it has a date
// of that kind, else from FallbackRegion (US), else the earliest date of any
// region (ties broken by region code). Cinema uses the theatrical date, falling
// back to limited theatrical then premiere (see ReleaseDates.Cinema).
// The certification comes from the configured region, else US, else none: a
// rating from an arbitrary country would be misleading.
func SelectReleases(all []ReleaseDates, region string) ReleaseSelection {
	region = strings.ToUpper(region)
	var sel ReleaseSelection
	sel.Cinema = pick(all, region, func(r *ReleaseDates) (Date, ReleaseType) { return r.Cinema() })
	sel.Digital = pick(all, region, func(r *ReleaseDates) (Date, ReleaseType) { return r.Digital(), ReleaseDigital })
	sel.Physical = pick(all, region, func(r *ReleaseDates) (Date, ReleaseType) { return r.Physical(), ReleasePhysical })
	for _, want := range [...]string{region, FallbackRegion} {
		if want == "" {
			continue
		}
		for i := range all {
			if strings.EqualFold(all[i].Region, want) {
				if c := all[i].Certification(); c != "" {
					sel.Certification, sel.CertificationRegion = c, strings.ToUpper(all[i].Region)
					return sel
				}
			}
		}
	}
	return sel
}

func pick(all []ReleaseDates, region string, get func(*ReleaseDates) (Date, ReleaseType)) DatedRelease {
	for _, want := range [...]string{region, FallbackRegion} {
		if want == "" {
			continue
		}
		for i := range all {
			if strings.EqualFold(all[i].Region, want) {
				if d, t := get(&all[i]); !d.IsZero() {
					return DatedRelease{Date: d, Region: strings.ToUpper(all[i].Region), Type: t}
				}
			}
		}
	}
	var best DatedRelease
	for i := range all {
		d, t := get(&all[i])
		if d.IsZero() {
			continue
		}
		r := strings.ToUpper(all[i].Region)
		if best.Date.IsZero() || d.Before(best.Date) || (d == best.Date && r < best.Region) {
			best = DatedRelease{Date: d, Region: r, Type: t}
		}
	}
	return best
}

// SortReleaseDates orders regions by code, and dates in a region by date then type.
func SortReleaseDates(all []ReleaseDates) {
	slices.SortFunc(all, func(a, b ReleaseDates) int { return strings.Compare(a.Region, b.Region) })
	for i := range all {
		slices.SortStableFunc(all[i].Dates, func(a, b ReleaseDate) int {
			if c := a.Date.Compare(b.Date); c != 0 {
				return c
			}
			return int(a.Type) - int(b.Type)
		})
	}
}

// AssumedHomeReleaseDays is how long after its cinema release a movie without
// any known digital or physical date is assumed released.
const AssumedHomeReleaseDays = 90

// DeriveStatus computes the status of a movie from its chosen dates at time now,
// with a plain-language reason.
//
//   - released: a digital or physical date is today or past; or, when neither is
//     known, the cinema date is at least AssumedHomeReleaseDays old (old movies
//     often have no home release date on record).
//   - in_cinemas: the cinema date is today or past.
//   - announced: otherwise, including when no date is known at all.
//
// primary (the provider's primary release date) stands in for the cinema date
// when no regional cinema date is known. Dates are compared in UTC.
func DeriveStatus(sel ReleaseSelection, primary Date, now time.Time) (status Status, reason string) {
	today := DateOf(now.UTC())
	reached := func(d Date) bool { return !d.IsZero() && d.Compare(today) <= 0 }

	if reached(sel.Digital.Date) {
		return StatusReleased, "digital release on " + describe(sel.Digital)
	}
	if reached(sel.Physical.Date) {
		return StatusReleased, "physical release on " + describe(sel.Physical)
	}
	cinema := sel.Cinema
	if cinema.Date.IsZero() && !primary.IsZero() {
		cinema = DatedRelease{Date: primary}
	}
	if reached(cinema.Date) {
		if sel.Digital.Date.IsZero() && sel.Physical.Date.IsZero() &&
			reached(cinema.Date.AddDays(AssumedHomeReleaseDays)) {
			return StatusReleased, fmt.Sprintf("no digital or physical date known; assumed released %d days after the cinema release on %s",
				AssumedHomeReleaseDays, describe(cinema))
		}
		return StatusInCinemas, "in cinemas since " + describe(cinema)
	}
	if !cinema.Date.IsZero() {
		return StatusAnnounced, "cinema release planned on " + describe(cinema)
	}
	return StatusAnnounced, "no release date known"
}

func describe(d DatedRelease) string {
	s := d.Date.String()
	switch {
	case d.Region != "" && d.Type != 0:
		return fmt.Sprintf("%s (%s, %s)", s, d.Region, d.Type)
	case d.Region != "":
		return fmt.Sprintf("%s (%s)", s, d.Region)
	}
	return s
}
