package main

import (
	"strings"
	"testing"
	"time"
)

func TestSunSigns(t *testing.T) {
	cases := map[string]string{"2001-03-21": "aries", "2001-04-19": "aries", "2001-04-20": "taurus", "2001-06-21": "cancer", "2001-08-22": "leo", "2001-09-23": "libra", "2001-10-30": "scorpio", "2001-12-22": "capricorn", "2001-01-19": "capricorn", "2001-02-19": "pisces"}
	for date, want := range cases {
		d, err := time.Parse("2006-01-02", date)
		if err != nil {
			t.Fatal(err)
		}
		if got := sunSign(d); got != want {
			t.Errorf("%s got %q want %q", date, got, want)
		}
	}
}
func TestMatchIsStableAndSymmetric(t *testing.T) {
	req := matchRequest{personInput{"Alex", "2000-07-30"}, personInput{"Taylor", "1999-11-03"}}
	a, e := buildMatch(req)
	if e != nil {
		t.Fatal(e)
	}
	b, e := buildMatch(matchRequest{req.Second, req.First})
	if e != nil {
		t.Fatal(e)
	}
	if a.Score != b.Score {
		t.Errorf("score should be symmetric: %d != %d", a.Score, b.Score)
	}
	if a.Score < 48 || a.Score > 96 {
		t.Errorf("out of range: %d", a.Score)
	}
	if len(a.Insights) != 3 {
		t.Fatal("missing insights")
	}
}
func TestRejectBadDates(t *testing.T) {
	for _, date := range []string{"2027-01-01", "1899-12-30", "2026-02-30", "2035-08-21", "today"} {
		_, err := buildMatch(matchRequest{personInput{"Alex", date}, personInput{"Taylor", "2000-02-02"}})
		if err == nil {
			t.Errorf("date %s should fail", date)
		}
	}
}
func TestNeverExposeBirthDatesInResponse(t *testing.T) {
	out, err := buildMatch(matchRequest{personInput{"Avery", "2001-08-21"}, personInput{"Jordan", "2003-03-18"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Description, "2001-") {
		t.Fatal("response contains birth date")
	}
}
func TestTrafficSources(t *testing.T) {
	for _, tc := range []struct{ utm, ref, want string }{{"tiktok", "", "TikTok"}, {"", "https://www.youtube.com", "YouTube"}, {"", "", "Direct / unknown"}, {"my_creator", "", "my_creator"}} {
		if got := sourceName(tc.utm, tc.ref); got != tc.want {
			t.Errorf("sourceName(%q,%q)=%q want %q", tc.utm, tc.ref, got, tc.want)
		}
	}
}
func TestEventAggregation(t *testing.T) {
	now := time.Now().UTC()
	events := []event{
		{CreatedAt: now, EventType: "page_view", SessionHash: "a", Country: "US", Source: "TikTok"},
		{CreatedAt: now, EventType: "page_view", SessionHash: "b", Country: "KZ", Source: "Instagram"},
		{CreatedAt: now, EventType: "complete_match", SessionHash: "a", Country: "US", Source: "TikTok"},
		{CreatedAt: now, EventType: "share_match", SessionHash: "a", Country: "US", Source: "TikTok"},
	}
	result := makeStats(events, 7, false, "local")
	if result.Views != 2 || result.Sessions != 2 || result.Matches != 1 || result.Shares != 1 || len(result.Countries) != 2 {
		t.Fatalf("wrong aggregation: %+v", result)
	}
}
