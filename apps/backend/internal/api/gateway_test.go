package api

import (
	"net/http"
	"net/url"
	"testing"
)

func TestHardenSchedulerResponse(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Set-Cookie":              {"x=1"},
			"X-Frame-Options":         {"ALLOWALL"},
			"Content-Security-Policy": {"frame-ancestors *"},
			"X-Content-Type-Options":  {"sniff"},
		},
		Request: &http.Request{URL: &url.URL{Path: "/scheduler/apps/shop/api/v1/jobs"}},
	}
	if err := hardenSchedulerResponse(resp); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Set-Cookie":                   "",
		"X-Frame-Options":              "SAMEORIGIN",
		"Content-Security-Policy":      "frame-ancestors 'self'",
		"X-Content-Type-Options":       "nosniff",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cache-Control":                "no-store",
	}
	for k, v := range want {
		got := resp.Header.Values(k)
		if (v == "" && len(got) != 0) || (v != "" && (len(got) != 1 || got[0] != v)) {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestOriginAllowedRefusesOpaque(t *testing.T) {
	s := &Server{AllowedOrigins: []string{"null", "", "http://127.0.0.1:7070"}}
	for _, o := range []string{"null", ""} {
		if s.originAllowed(o) {
			t.Errorf("origin %q allowed", o)
		}
	}
	if !s.originAllowed("http://127.0.0.1:7070") {
		t.Error("configured origin refused")
	}
}
