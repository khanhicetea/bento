package traffic

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// testdata/goaccess-1.12.json is real output of the pinned GoAccess run with
// Args over two generated logs of the edge's bento_traffic format (100 lines
// and one unparseable line).
func TestParseGoAccessReport(t *testing.T) {
	b, err := os.ReadFile("testdata/goaccess-1.12.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.From != "2026-11-03" || r.To != "2026-11-04" {
		t.Fatalf("period = %s..%s", r.From, r.To)
	}
	if r.Requests != 100 || r.Invalid != 1 || r.Visitors != 84 || r.Bytes != 2678517 {
		t.Fatalf("totals = %+v", r)
	}
	if len(r.Days) != 2 || r.Days[0] != (Count{"2026-11-03", 60, r.Days[0].Visitors, r.Days[0].Bytes}) ||
		r.Days[1].Label != "2026-11-04" || r.Days[1].Requests != 40 {
		t.Fatalf("days = %+v", r.Days)
	}
	labels := func(cs []Count) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Label)
		}
		return out
	}
	if got := labels(r.Statuses); !slices.Equal(got, []string{"2xx", "5xx", "4xx", "3xx"}) {
		t.Fatalf("statuses = %v", got)
	}
	if r.Codes[0].Label != "200" || r.Codes[0].Requests != 74 || r.Codes[1].Label != "502" || r.Codes[1].Requests != 6 {
		t.Fatalf("codes = %+v", r.Codes)
	}
	top := r.Paths[0]
	if top.Method != "GET" || top.Path != "/" || top.Requests != 17 || top.AvgMicros != 973647 || top.MaxMicros != 1877000 {
		t.Fatalf("top path = %+v", top)
	}
	if got := labels(r.Hours); !slices.Equal(got, []string{"10"}) {
		t.Fatalf("hours = %v", got)
	}
	if !slices.Contains(labels(r.Agents), "Crawlers") {
		t.Fatalf("agents = %v", labels(r.Agents))
	}
	if r.Clients[0].Label != "203.0.113.6" || len(r.Clients) != 38 {
		t.Fatalf("clients = %+v", r.Clients[:1])
	}
	if got := labels(r.Referrers); !slices.Equal(got, []string{"www.google.com", "news.ycombinator.com", "shop.example.com"}) {
		t.Fatalf("referrers = %v", got)
	}
	for i := 1; i < len(r.Paths); i++ {
		if r.Paths[i].Requests > r.Paths[i-1].Requests {
			t.Fatalf("paths not ordered by requests at %d", i)
		}
	}
}

func TestParseRejectsNonReports(t *testing.T) {
	for _, in := range []string{"", "{}", "[]", "not json"} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Fatalf("Parse(%q) accepted", in)
		}
	}
}

func TestArgsReadOnlyPendingFilesInsideTheMount(t *testing.T) {
	args := Args([]string{"20261105T100000Z.log"})
	for _, want := range []string{"--restore", "--persist", "--no-query-string", "--keep-last=30", "--db-path=/traffic/db",
		"--output=/traffic/report.next.json", "/traffic/pending/20261105T100000Z.log"} {
		if !slices.Contains(args, want) {
			t.Fatalf("args lack %q: %v", want, args)
		}
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "--") && !strings.HasPrefix(a, Mount+"/"+PendingDir+"/") {
			t.Fatalf("unexpected positional argument %q", a)
		}
	}
	// Without pending logs GoAccess only rewrites the report from the
	// database: no positional argument (it must not wait on stdin).
	for _, a := range Args(nil) {
		if !strings.HasPrefix(a, "--") {
			t.Fatalf("restore-only run has positional argument %q", a)
		}
	}
}

func TestPendingNamesSortByTime(t *testing.T) {
	a := PendingName(time.Date(2026, 11, 5, 9, 2, 0, 0, time.UTC))
	b := PendingName(time.Date(2026, 11, 5, 10, 2, 0, 0, time.FixedZone("x", 7*3600)))
	if a != "20261105T090200Z.log" || b != "20261105T030200Z.log" || b >= a {
		t.Fatalf("names = %s %s", a, b)
	}
	if at, ok := PendingTime(a); !ok || !at.Equal(time.Date(2026, 11, 5, 9, 2, 0, 0, time.UTC)) {
		t.Fatalf("PendingTime(%s) = %v %v", a, at, ok)
	}
	for _, bad := range []string{"report.json", "x.log", "../20261105T090200Z.log"} {
		if _, ok := PendingTime(bad); ok {
			t.Fatalf("PendingTime(%q) accepted", bad)
		}
	}
}
