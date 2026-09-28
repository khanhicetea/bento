package api

import (
	"testing"
	"time"
)

func TestParseStubStatus(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nServer: nginx\r\n\r\nActive connections: 3 \nserver accepts handled requests\n 12 10 40 \nReading: 0 Writing: 1 Waiting: 2 \n"
	m, err := parseStubStatus([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if m.Active != 3 || m.Accepts != 12 || m.Handled != 10 || m.Requests != 40 || m.Reading != 0 || m.Writing != 1 || m.Waiting != 2 || m.Dropped != 2 {
		t.Fatalf("unexpected %+v", m)
	}
	if _, err := parseStubStatus([]byte("garbage")); err == nil {
		t.Fatal("expected error")
	}
}

func TestEdgeSamplerRates(t *testing.T) {
	var e edgeSampler
	t0 := time.Now()
	if r, _ := e.rates(edgeSample{at: t0, requests: 100}); r != 0 {
		t.Fatal("first sample must be zero")
	}
	if r, _ := e.rates(edgeSample{at: t0.Add(2 * time.Second), requests: 120}); r != 10 {
		t.Fatalf("got %v", r)
	}
	if r, _ := e.rates(edgeSample{at: t0.Add(4 * time.Second), requests: 5}); r != 0 {
		t.Fatal("reset must be zero")
	}
}
