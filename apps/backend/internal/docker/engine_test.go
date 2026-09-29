package docker

import (
	"errors"
	"testing"
)

func TestEnsureImage(t *testing.T) {
	ctx := t.Context()
	errPull := errors.New("registry down")
	errBefore := errors.New("cancelled")
	cases := map[string]struct {
		present    bool
		failPull   bool
		before     error
		wantErr    error
		wantBefore bool
		wantPulls  int
	}{
		"present":           {present: true},
		"missing":           {wantBefore: true, wantPulls: 1},
		"pull fails":        {failPull: true, wantErr: errPull, wantBefore: true, wantPulls: 1},
		"beforePull aborts": {before: errBefore, wantErr: errBefore, wantBefore: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := NewFake()
			if tc.present {
				f.Images["img:1"] = "sha256:1"
			}
			if tc.failPull {
				f.FailOn = map[string]error{"PullImage": errPull}
			}
			var called bool
			err := EnsureImage(ctx, f, "img:1", func() error { called = true; return tc.before })
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
			if called != tc.wantBefore || f.CallCount("PullImage img:1") != tc.wantPulls {
				t.Fatalf("beforePull %v pulls %d", called, f.CallCount("PullImage"))
			}
		})
	}
	if err := EnsureImage(ctx, NewFake(), "img:2", nil); err != nil {
		t.Fatalf("nil beforePull: %v", err)
	}
}
