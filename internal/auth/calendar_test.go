package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCalendarOptionsValidate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		o     CalendarOptions
		field string
		types []string
	}{
		{"defaults", DefaultCalendarOptions(), "", []string{"cinema", "digital", "physical"}},
		{"normalized", CalendarOptions{PastDays: 0, FutureDays: 1, Types: []string{"physical", " cinema", "physical"}}, "", []string{"cinema", "physical"}},
		{"past", CalendarOptions{PastDays: 366, FutureDays: 10, Types: []string{"cinema"}}, "pastDays", nil},
		{"future", CalendarOptions{PastDays: 0, FutureDays: 0, Types: []string{"cinema"}}, "futureDays", nil},
		{"no type", CalendarOptions{FutureDays: 10}, "releaseTypes", nil},
		{"bad type", CalendarOptions{FutureDays: 10, Types: []string{"tv"}}, "releaseTypes", nil},
	} {
		err := tc.o.Validate()
		var inv *InvalidError
		switch {
		case tc.field == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.field != "" && (!errors.As(err, &inv) || inv.Field != tc.field):
			t.Errorf("%s: err = %v, want field %s", tc.name, err, tc.field)
		case tc.field == "" && !slices.Equal(tc.o.Types, tc.types):
			t.Errorf("%s: types = %v", tc.name, tc.o.Types)
		}
	}
}

func TestCalendarFeedKey(t *testing.T) {
	s, c, _ := newTest(t)
	ctx := context.Background()
	cr, err := s.Setup(ctx, "admin", "long enough password", ClientInfo{IP: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	uid := cr.User.ID
	f, err := s.CalendarFeed(ctx, uid)
	if err != nil || f.Enabled || f.Options.FutureDays != 180 {
		t.Fatalf("initial feed = %+v, %v", f, err)
	}
	if _, err := s.CalendarFeed(ctx, 0); !errors.Is(err, ErrNeedsSetup) {
		t.Errorf("no account: %v", err)
	}
	if err := s.DisableCalendarFeed(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Errorf("disable an off feed: %v", err)
	}
	if f, err = s.SetCalendarOptions(ctx, uid, CalendarOptions{PastDays: 7, FutureDays: 30, Unmonitored: true, Types: []string{"digital"}}); err != nil || f.Enabled || !f.Options.Unmonitored {
		t.Fatalf("options = %+v, %v", f, err)
	}
	key, f, err := s.RegenerateCalendarKey(ctx, uid)
	if err != nil || !strings.HasPrefix(key, CalendarKeyPrefix) || !f.Enabled || f.Last4 != key[len(key)-4:] || f.Options.PastDays != 7 {
		t.Fatalf("regenerate = %q %+v, %v", key, f, err)
	}
	// The key opens the feed only: it is not an API key.
	if _, err := s.APIKeyPrincipal(ctx, key); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("calendar key accepted as an API key: %v", err)
	}
	if _, err := s.CalendarKeyOptions(ctx, cr.APIKey); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("API key accepted as a calendar key: %v", err)
	}
	o, err := s.CalendarKeyOptions(ctx, key)
	if err != nil || o.FutureDays != 30 || !slices.Equal(o.Types, []string{"digital"}) {
		t.Fatalf("options of the key = %+v, %v", o, err)
	}
	if f, _ = s.CalendarFeed(ctx, uid); f.LastUsedAt == nil {
		t.Error("last use not recorded")
	}
	// Regenerating revokes the previous key.
	c.add(time.Minute)
	key2, _, err := s.RegenerateCalendarKey(ctx, uid)
	if err != nil || key2 == key {
		t.Fatal(err)
	}
	if _, err := s.CalendarKeyOptions(ctx, key); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("old key still works: %v", err)
	}
	if err := s.DisableCalendarFeed(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CalendarKeyOptions(ctx, key2); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("key works after disable: %v", err)
	}
	if f, _ = s.CalendarFeed(ctx, uid); f.Enabled || f.Last4 != "" || f.Options.PastDays != 7 {
		t.Errorf("after disable = %+v (options kept)", f)
	}
}

// TestSessionVia: the connection kind is stored at login and refreshed on use.
func TestSessionVia(t *testing.T) {
	s, c, _ := newTest(t)
	ctx := context.Background()
	cr, err := s.Setup(ctx, "admin", "long enough password", ClientInfo{IP: "192.168.1.5", Via: ViaLocal})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Sessions(ctx, cr.User.ID)
	if err != nil || len(list) != 1 || list[0].Via != ViaLocal {
		t.Fatalf("sessions = %+v, %v", list, err)
	}
	if _, err := s.SessionPrincipalFor(ctx, cr.Token, ClientInfo{IP: "203.0.113.9", Via: ViaProxy}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Sessions(ctx, cr.User.ID)
	if list[0].Via != ViaProxy || list[0].IP != "203.0.113.9" {
		t.Errorf("after use through the proxy = %+v", list[0])
	}
	c.add(time.Second)
	if _, err := s.SessionPrincipalFor(ctx, cr.Token, ClientInfo{IP: "203.0.113.9", Via: "bogus"}); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.Sessions(ctx, cr.User.ID); list[0].Via != ViaDirect {
		t.Errorf("unknown via = %q, want direct", list[0].Via)
	}
}
