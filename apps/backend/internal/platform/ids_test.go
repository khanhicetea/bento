package platform

import (
	"bytes"
	"testing"
)

// TestSHA256HexConcatMatchesJoined pins the streaming form to the joined form
// it replaced: both feed generation fingerprints, so a difference would
// replace every running app.
func TestSHA256HexConcatMatchesJoined(t *testing.T) {
	big := bytes.Repeat([]byte("server { listen 8080; }\n"), 400)
	cases := [][][]byte{
		nil,
		{nil, nil},
		{[]byte("frontend"), nil},
		{nil, []byte("fastcgi")},
		{[]byte("nginx.conf"), []byte("fastcgi.conf")},
		{big, big[:17], []byte("x")},
	}
	for _, parts := range cases {
		if got, want := SHA256HexConcat(parts...), SHA256Hex(bytes.Join(parts, nil)); got != want {
			t.Fatalf("SHA256HexConcat(%d parts) = %s, want %s", len(parts), got, want)
		}
	}
	// Known vector: SHA-256 of "abc" split across parts.
	const abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := SHA256HexConcat([]byte("a"), []byte("bc")); got != abc {
		t.Fatalf("SHA256HexConcat(a, bc) = %s", got)
	}
}

func BenchmarkSHA256HexConcat(b *testing.B) {
	frontend := bytes.Repeat([]byte("location / { try_files $uri /index.php?$query_string; }\n"), 60)
	fastcgi := bytes.Repeat([]byte("fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n"), 30)
	b.Run("joined", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = SHA256Hex(append(append([]byte{}, frontend...), fastcgi...))
		}
	})
	b.Run("streaming", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = SHA256HexConcat(frontend, fastcgi)
		}
	})
}
