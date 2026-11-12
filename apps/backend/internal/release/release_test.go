package release

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves /releases/latest, SHA256SUMS, one asset and one raw file.
func fakeGitHub(t *testing.T, tag string, asset []byte, sums string) Source {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/o/r/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("GET /o/r/releases/download/"+tag+"/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(sums))
	})
	mux.HandleFunc("GET /o/r/releases/download/"+tag+"/bento-linux-amd64", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(asset)
	})
	mux.HandleFunc("GET /raw/o/r/"+tag+"/deploy/systemd/bento@.service", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("[Unit]\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return Source{Repo: "o/r", Base: srv.URL, RawBase: srv.URL + "/raw", HTTP: srv.Client(), UserAgent: "bento-test"}
}

func sumOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestLatestReadsTheReleaseRedirect(t *testing.T) {
	s := fakeGitHub(t, "0.6.0", nil, "")
	tag, err := s.Latest(t.Context())
	if err != nil || tag != "0.6.0" {
		t.Fatalf("latest %q %v", tag, err)
	}
}

func TestDownloadVerifiesTheChecksum(t *testing.T) {
	bin := []byte("#!/bin/true\n")
	other := sumOf([]byte("arm64 build"))
	s := fakeGitHub(t, "0.6.0", bin, other+"  bento-linux-arm64\n"+sumOf(bin)+"  bento-linux-amd64\n")
	dir := t.TempDir()
	p, err := s.Download(t.Context(), "0.6.0", AssetName("amd64"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != string(bin) {
		t.Fatalf("downloaded %q", got)
	}
	if unit, err := s.File(t.Context(), "0.6.0", "deploy/systemd/bento@.service", 1<<16); err != nil ||
		string(unit) != "[Unit]\n" {
		t.Fatalf("unit %q %v", unit, err)
	}
}

func TestDownloadRefusesBadChecksumsAndTags(t *testing.T) {
	bin := []byte("tampered")
	cases := map[string]string{
		"mismatch":  sumOf([]byte("original")) + "  bento-linux-amd64\n",
		"missing":   sumOf(bin) + "  bento-linux-arm64\n",
		"malformed": "abc  bento-linux-amd64\n",
	}
	for name, sums := range cases {
		t.Run(name, func(t *testing.T) {
			s := fakeGitHub(t, "0.6.0", bin, sums)
			dir := t.TempDir()
			if _, err := s.Download(t.Context(), "0.6.0", AssetName("amd64"), dir); err == nil {
				t.Fatal("download accepted")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("a refused download must leave nothing behind: %v", entries)
			}
		})
	}
	s := fakeGitHub(t, "0.6.0", bin, "")
	for _, tag := range []string{"../../x", "latest", "0.6", "0.6.0/../a"} {
		if _, err := s.Download(t.Context(), tag, "bento-linux-amd64", t.TempDir()); err == nil ||
			!strings.Contains(err.Error(), "invalid release tag") {
			t.Errorf("tag %q: %v", tag, err)
		}
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("unexpected file")
	}
}

func TestCompareVersions(t *testing.T) {
	order := []string{"0.2.0-dev+ca14d6a", "0.2.0", "0.4.1+96c9ad9", "v0.5.0-rc1", "0.5.0", "0.10.0", "1.0.0"}
	for i := range order {
		for j := range order {
			a, ok1 := ParseVersion(order[i])
			b, ok2 := ParseVersion(order[j])
			if !ok1 || !ok2 {
				t.Fatalf("parse %q %q", order[i], order[j])
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "dev", "1.2", "1.2.x", "a.b.c"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
