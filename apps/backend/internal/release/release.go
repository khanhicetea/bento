// Package release finds and downloads Bento releases from GitHub. A binary is
// accepted only when its SHA-256 matches the SHA256SUMS file published with
// the same release tag. That checks the download, not who built it: the
// checksums come from the same release as the binaries and are not signed.
package release

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is the GitHub owner/repo releases come from.
const DefaultRepo = "khanhicetea/bento"

// MaxAssetBytes bounds a downloaded binary.
const MaxAssetBytes = 512 << 20

// Source is where releases are found.
type Source struct {
	// Repo is the GitHub owner/repo.
	Repo string
	// Base is the web origin (https://github.com); tests point it elsewhere.
	Base string
	// RawBase serves repository files at a tag (https://raw.githubusercontent.com).
	RawBase string
	// HTTP is the client; it must not send credentials.
	HTTP *http.Client
	// UserAgent is sent with every request.
	UserAgent string
}

// GitHub returns the source for repo (DefaultRepo when empty).
func GitHub(repo, userAgent string) Source {
	if repo == "" {
		repo = DefaultRepo
	}
	return Source{Repo: repo, Base: "https://github.com", RawBase: "https://raw.githubusercontent.com",
		HTTP: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: httpsOnly}, UserAgent: userAgent}
}

func httpsOnly(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect to %s", req.URL.Redacted())
	}
	return nil
}

var tagPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// ValidTag reports whether tag looks like a release tag (0.5.0, v0.5.0,
// 0.5.0-rc1). It keeps arbitrary text out of URLs and paths.
func ValidTag(tag string) bool { return tagPattern.MatchString(tag) }

// Latest returns the tag of the latest published release, read from the
// redirect of /releases/latest (no API token or rate-limited API call).
func (s Source) Latest(ctx context.Context) (string, error) {
	u := s.Base + "/" + s.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	c := *s.HTTP
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", fmt.Errorf("latest release of %s: HTTP %d (no published release?)", s.Repo, resp.StatusCode)
	}
	loc, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("latest release of %s: %w", s.Repo, err)
	}
	dir, tag := path.Split(loc.Path)
	if !strings.HasSuffix(dir, "/releases/tag/") {
		return "", fmt.Errorf("latest release of %s: unexpected redirect to %s", s.Repo, loc.Redacted())
	}
	if tag, err = url.PathUnescape(tag); err != nil || !ValidTag(tag) {
		return "", fmt.Errorf("latest release of %s: unexpected tag %q", s.Repo, tag)
	}
	return tag, nil
}

// AssetName is the release asset of the Linux binary for arch (amd64, arm64).
func AssetName(arch string) string { return "bento-linux-" + arch }

// Download fetches asset and SHA256SUMS of tag, and writes the asset to
// dir/asset (mode 0700) only if its SHA-256 matches the line for it.
func (s Source) Download(ctx context.Context, tag, asset, dir string) (string, error) {
	if !ValidTag(tag) {
		return "", fmt.Errorf("invalid release tag %q", tag)
	}
	base := s.Base + "/" + s.Repo + "/releases/download/" + url.PathEscape(tag) + "/"
	var sums strings.Builder
	if err := s.get(ctx, base+"SHA256SUMS", 1<<20, &sums); err != nil {
		return "", fmt.Errorf("SHA256SUMS of %s: %w", tag, err)
	}
	want, err := checksumFor(sums.String(), asset)
	if err != nil {
		return "", fmt.Errorf("SHA256SUMS of %s: %w", tag, err)
	}
	dest := filepath.Join(dir, asset)
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	err = s.get(ctx, base+asset, MaxAssetBytes, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = fmt.Errorf("checksum mismatch: SHA256SUMS says %s", want)
	}
	if err != nil {
		os.Remove(dest)
		return "", fmt.Errorf("%s of %s: %w", asset, tag, err)
	}
	return dest, nil
}

// File fetches one repository file at tag (for example the systemd unit).
// Like the release assets, it is trusted because it comes from the tag.
func (s Source) File(ctx context.Context, tag, name string, limit int64) ([]byte, error) {
	if !ValidTag(tag) {
		return nil, fmt.Errorf("invalid release tag %q", tag)
	}
	var b strings.Builder
	if err := s.get(ctx, s.RawBase+"/"+s.Repo+"/"+url.PathEscape(tag)+"/"+name, limit, &b); err != nil {
		return nil, fmt.Errorf("%s at %s: %w", name, tag, err)
	}
	return []byte(b.String()), nil
}

func (s Source) get(ctx context.Context, u string, limit int64, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("larger than %d bytes", limit)
	}
	return nil
}

// checksumFor returns the hex SHA-256 sha256sum lists for name.
func checksumFor(sums, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		sum, file, ok := strings.Cut(strings.TrimSpace(sc.Text()), "  ")
		file = strings.TrimPrefix(file, "*")
		if !ok || file != name {
			continue
		}
		if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
			return "", fmt.Errorf("malformed checksum for %s", name)
		}
		return strings.ToLower(sum), nil
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

// Version is a parsed X.Y.Z[-pre] version; build metadata (+commit) is
// dropped.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// ParseVersion parses a tag or a `bento version` string (0.5.0, v0.5.0,
// 0.5.0+96c9ad9, 0.2.0-dev+ca14d6a).
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return Version{}, false
		}
		n[i] = x
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2], Pre: pre}, true
}

// Compare orders versions: -1, 0 or 1. A pre-release sorts before its
// release; pre-releases of the same core compare as strings.
func Compare(a, b Version) int {
	for _, d := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	return strings.Compare(a.Pre, b.Pre)
}
