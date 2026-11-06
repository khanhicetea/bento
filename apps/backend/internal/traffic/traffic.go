// Package traffic defines host traffic reports: the GoAccess invocation that
// aggregates a host's rotated edge access logs, and the parsed report the API
// serves. It performs no I/O; operations runs GoAccess and api reads reports.
package traffic

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// Interval is how often the edge's traffic logs are rotated and
	// aggregated. Reports lag traffic by at most one interval.
	Interval = time.Hour
	// KeepDays is how many days of data a host's database keeps.
	KeepDays = 30
	// MaxItems caps each panel of a written report (not the database).
	MaxItems = 50
	// MaxPendingAge drops rotated logs GoAccess has not managed to read, so
	// a log it keeps failing on cannot grow the stack root without bound.
	MaxPendingAge = 7 * 24 * time.Hour

	// Mount is where a GoAccess run sees the host's traffic directory.
	Mount = "/traffic"
	// DBDir, PendingDir, ReportFile and NextReportFile are relative to a
	// host's traffic directory. GoAccess picks its output format from the
	// .json extension.
	DBDir          = "db"
	PendingDir     = "pending"
	ReportFile     = "report.json"
	NextReportFile = "report.next.json"
)

// LogFormat matches the edge's bento_traffic log_format.
const (
	LogFormat  = `%h [%d:%t %^] "%m %U %H" %s %b "%R" "%u" %T`
	DateFormat = "%d/%b/%Y"
	TimeFormat = "%H:%M:%S"
)

// ignoredPanels are not stored: the edge already drops static files and 404s,
// and the rest are not shown.
var ignoredPanels = []string{
	"OS", "REFERRERS", "KEYPHRASES", "GEO_LOCATION", "ASN", "REMOTE_USER", "CACHE_STATUS",
	"MIME_TYPE", "TLS_TYPE", "NOT_FOUND", "STATIC_REQUESTS", "UTM_CAMPAIGNS",
}

// Args is the GoAccess command line that adds the pending logs (names in
// PendingDir) to the host's database and writes the next report. With no
// logs it only rewrites the report from the database, which also drops days
// older than KeepDays. GoAccess remembers each file's inode and last line, so
// re-reading a log that was already added (a run interrupted before its
// pending files were removed) adds nothing.
func Args(pending []string) []string {
	args := []string{
		"--no-global-config",
		"--log-format=" + LogFormat,
		"--date-format=" + DateFormat,
		"--time-format=" + TimeFormat,
		"--no-query-string",
		"--restore", "--persist",
		"--db-path=" + Mount + "/" + DBDir,
		"--keep-last=" + strconv.Itoa(KeepDays),
		"--max-items=" + strconv.Itoa(MaxItems),
		"--no-progress",
		"--output=" + Mount + "/" + NextReportFile,
	}
	for _, p := range ignoredPanels {
		args = append(args, "--ignore-panel="+p)
	}
	for _, name := range pending {
		args = append(args, Mount+"/"+PendingDir+"/"+name)
	}
	return args
}

// PendingName is the name of a log rotated at t. Names sort by time.
func PendingName(t time.Time) string { return t.UTC().Format("20060102T150405Z") + ".log" }

// PendingTime parses a PendingName; ok is false for any other name.
func PendingTime(name string) (time.Time, bool) {
	stem, found := strings.CutSuffix(name, ".log")
	if !found {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102T150405Z", stem)
	return t, err == nil
}

// Count is one row of a report panel.
type Count struct {
	Label    string
	Requests int64
	Visitors int64
	Bytes    int64
}

// Path is one row of the requests panel. Times are in microseconds.
type Path struct {
	Method    string
	Path      string
	Requests  int64
	Visitors  int64
	Bytes     int64
	AvgMicros int64
	MaxMicros int64
}

// Report is the part of a GoAccess JSON report Bento shows.
type Report struct {
	// From and To are the first and last day with data (YYYY-MM-DD).
	From, To string
	Requests int64
	Visitors int64
	Bytes    int64
	// Invalid counts log lines GoAccess could not parse.
	Invalid int64
	// Days are oldest first; Hours are "00"-"23" in order.
	Days  []Count
	Hours []Count
	// Statuses are classes ("2xx"), Codes single codes ("502"); both by
	// requests, most first, as are the remaining panels.
	Statuses  []Count
	Codes     []Count
	Paths     []Path
	Clients   []Count
	Agents    []Count
	Referrers []Count
}

type gaMetric struct {
	Count int64 `json:"count"`
}

type gaItem struct {
	Hits     gaMetric `json:"hits"`
	Visitors gaMetric `json:"visitors"`
	Bytes    gaMetric `json:"bytes"`
	AvgTS    int64    `json:"avgts"`
	MaxTS    int64    `json:"maxts"`
	Method   string   `json:"method"`
	Data     string   `json:"data"`
	Items    []gaItem `json:"items"`
}

type gaPanel struct {
	Data []gaItem `json:"data"`
}

type gaReport struct {
	General *struct {
		StartDate      string `json:"start_date"`
		EndDate        string `json:"end_date"`
		TotalRequests  int64  `json:"total_requests"`
		FailedRequests int64  `json:"failed_requests"`
		UniqueVisitors int64  `json:"unique_visitors"`
		Bandwidth      int64  `json:"bandwidth"`
	} `json:"general"`
	Visitors       gaPanel `json:"visitors"`
	Requests       gaPanel `json:"requests"`
	Hosts          gaPanel `json:"hosts"`
	Browsers       gaPanel `json:"browsers"`
	VisitTime      gaPanel `json:"visit_time"`
	ReferringSites gaPanel `json:"referring_sites"`
	StatusCodes    gaPanel `json:"status_codes"`
}

// Parse reads a GoAccess JSON report.
func Parse(b []byte) (Report, error) {
	var g gaReport
	if err := json.Unmarshal(b, &g); err != nil {
		return Report{}, fmt.Errorf("traffic report: %w", err)
	}
	if g.General == nil {
		return Report{}, errors.New("traffic report: no general section")
	}
	r := Report{
		From:     reportDate(g.General.StartDate),
		To:       reportDate(g.General.EndDate),
		Requests: g.General.TotalRequests - g.General.FailedRequests,
		Visitors: g.General.UniqueVisitors,
		Bytes:    g.General.Bandwidth,
		Invalid:  g.General.FailedRequests,
	}
	for _, it := range g.Visitors.Data {
		c := count(it)
		if t, err := time.Parse("20060102", it.Data); err == nil {
			c.Label = t.Format(time.DateOnly)
		}
		r.Days = append(r.Days, c)
	}
	slices.SortFunc(r.Days, func(a, b Count) int { return strings.Compare(a.Label, b.Label) })
	for _, it := range g.VisitTime.Data {
		r.Hours = append(r.Hours, count(it))
	}
	slices.SortFunc(r.Hours, func(a, b Count) int { return strings.Compare(a.Label, b.Label) })
	for _, it := range g.StatusCodes.Data {
		c := count(it)
		c.Label = firstField(it.Data) // "2xx Success"
		r.Statuses = append(r.Statuses, c)
		for _, code := range it.Items {
			c := count(code)
			c.Label = firstField(code.Data) // "502 - Bad Gateway: …"
			r.Codes = append(r.Codes, c)
		}
	}
	for _, it := range g.Requests.Data {
		r.Paths = append(r.Paths, Path{
			Method: it.Method, Path: it.Data,
			Requests: it.Hits.Count, Visitors: it.Visitors.Count, Bytes: it.Bytes.Count,
			AvgMicros: it.AvgTS, MaxMicros: it.MaxTS,
		})
	}
	r.Clients = counts(g.Hosts.Data)
	r.Agents = counts(g.Browsers.Data)
	r.Referrers = counts(g.ReferringSites.Data)
	byRequests := func(a, b Count) int { return cmp.Compare(b.Requests, a.Requests) }
	slices.SortStableFunc(r.Statuses, byRequests)
	slices.SortStableFunc(r.Codes, byRequests)
	slices.SortStableFunc(r.Clients, byRequests)
	slices.SortStableFunc(r.Agents, byRequests)
	slices.SortStableFunc(r.Referrers, byRequests)
	slices.SortStableFunc(r.Paths, func(a, b Path) int { return cmp.Compare(b.Requests, a.Requests) })
	return r, nil
}

func count(it gaItem) Count {
	return Count{Label: it.Data, Requests: it.Hits.Count, Visitors: it.Visitors.Count, Bytes: it.Bytes.Count}
}

func counts(items []gaItem) []Count {
	out := make([]Count, 0, len(items))
	for _, it := range items {
		out = append(out, count(it))
	}
	return out
}

func firstField(s string) string {
	f, _, _ := strings.Cut(strings.TrimSpace(s), " ")
	return f
}

// reportDate turns GoAccess's "03/Nov/2026" into "2026-11-03"; anything else
// is "" (unknown).
func reportDate(s string) string {
	t, err := time.Parse("02/Jan/2006", s)
	if err != nil {
		return ""
	}
	return t.Format(time.DateOnly)
}
