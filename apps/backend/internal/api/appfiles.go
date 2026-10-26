package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
)

// App code files: list, read and edit files under ~/app (the code directory)
// for admin callers and MCP. Never the rest of the home (config, .ssh, data)
// and never a hidden path (.env, .git): domain.CleanRelative refuses those,
// and the scripts check again after resolving symlinks.
//
// Every access is a fixed bash script exec'd in the app's container as the
// app's UID through bento-exec, with the path passed as an argument, so the
// backend never touches app-controlled paths as root. A stopped app gets a
// short-lived tool container, like exec. Writes go to a temp file in the same
// directory and are renamed over the target, and replacing a file needs the
// sha256 it was read with, so a stale edit is refused rather than applied.
// Like exec, these act directly and are not operations.

const (
	appFileReadDefault = 64 << 10
	appFileReadMax     = 256 << 10
	appFileEditMax     = 1 << 20
	appFileListMax     = 1000
	appFileTimeout     = time.Minute
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// appFilesPrelude is shared by every script. $1 is the code directory; $2 the
// cleaned relative path. resolve prints the real path of an existing entry
// and fails unless it stays inside the code directory and outside hidden
// paths. Exit codes are mapped by appFilesExec.
const appFilesPrelude = `set -euo pipefail
root=$(realpath -e -- "$1"); rel=$2; shift 2
fail() { echo "$2" >&2; exit "$1"; }
inside() {
  case "$1" in "$root") return 0 ;; "$root"/*) ;; *) return 1 ;; esac
  case "/${1#"$root"/}" in */.*) return 1 ;; esac
}
resolve() {
  local t="$root" p
  [ -n "$1" ] && t="$root/$1"
  [ -e "$t" ] || [ -L "$t" ] || fail 10 "no such file or directory: ${1:-.}"
  p=$(realpath -e -- "$t" 2>/dev/null) || fail 10 "broken symlink: $1"
  inside "$p" || fail 11 "path resolves outside the app directory or into a hidden path: $1"
  printf '%s' "$p"
}
`

const appFilesListScript = appFilesPrelude + `p=$(resolve "$rel")
[ -d "$p" ] || fail 12 "not a directory: ${rel:-.}"
set +o pipefail
find "$p" -mindepth 1 -maxdepth 1 ! -name '.*' -printf '%y\t%s\t%T@\t%f\0' | head -z -n "$1"
`

const appFilesReadScript = appFilesPrelude + `p=$(resolve "$rel")
[ -f "$p" ] || fail 12 "not a regular file: $rel"
size=$(stat -c %s -- "$p"); sum=$(sha256sum -- "$p"); sum=${sum%% *}
printf '%s %s\n' "$size" "$sum"
set +o pipefail
tail -c +$(( $1 + 1 )) -- "$p" | head -c "$2"
`

// $1 is create or replace, $2 the expected sha256; content is on stdin.
const appFilesWriteScript = appFilesPrelude + `dir=$(dirname -- "$rel"); base=$(basename -- "$rel")
[ "$dir" = . ] && dir=""
pd=$(resolve "$dir")
[ -d "$pd" ] || fail 12 "not a directory: $dir"
t="$pd/$base"
created=0
[ -L "$t" ] && fail 11 "refusing to write through a symlink: $rel"
if [ -e "$t" ]; then
  [ -f "$t" ] || fail 12 "not a regular file: $rel"
  [ "$1" = create ] && fail 14 "file exists: $rel; read it and pass its sha256 to replace it"
  cur=$(sha256sum -- "$t"); cur=${cur%% *}
  [ "$cur" = "$2" ] || fail 13 "file changed since it was read (sha256 now $cur); read it again"
else
  [ "$1" = replace ] && fail 10 "no such file: $rel"
  created=1
fi
tmp=$(mktemp -- "$pd/.bento-edit.XXXXXX")
trap 'rm -f -- "$tmp"' EXIT
cat > "$tmp"
if [ "$created" = 1 ]; then chmod 0644 -- "$tmp"; else chmod --reference="$t" -- "$tmp"; fi
mv -f -- "$tmp" "$t"
trap - EXIT
size=$(stat -c %s -- "$t"); sum=$(sha256sum -- "$t"); sum=${sum%% *}
printf '%s %s %s\n' "$size" "$sum" "$created"
`

// appFilePath reads and cleans a path relative to ~/app.
func appFilePath(p string, allowRoot bool) (string, error) {
	rel, err := domain.CleanRelative(p)
	if err != nil {
		return "", jobFieldError("path", err.Error())
	}
	if rel == "" && !allowRoot {
		return "", jobFieldError("path", "a file path inside the app directory is required")
	}
	return rel, nil
}

// appFilesExec runs one script in the app's running instance, or in a tool
// container when it is not running, and maps the script's exit codes.
func (s *Server) appFilesExec(ctx context.Context, app domain.App, script, rel string, args []string,
	stdin io.Reader, outLimit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, appFileTimeout)
	defer cancel()
	argv := append([]string{"bash", "-c", script, "bento-files", app.ContainerCode(), rel}, args...)
	er, err := operations.ExecRequestFor(app, argv, "")
	if err != nil {
		return nil, err
	}
	er.Stdin = stdin
	er.OutputLimit = outLimit
	target, err := s.C.RunningInstance(ctx, app)
	if err != nil {
		tool, terr := s.C.OpenTool(ctx, app, appFileTimeout)
		if terr != nil {
			return nil, terr
		}
		defer tool.Close()
		target = tool.ContainerID
	}
	res, err := s.C.Engine.Exec(ctx, target, er)
	if err != nil {
		return nil, err
	}
	if res.ExitCode == 0 {
		if res.Truncated {
			return nil, &apiError{status: http.StatusBadGateway, code: dto.ErrorCodeTooLarge, msg: "file output too large"}
		}
		return res.Stdout, nil
	}
	msg := strings.TrimSpace(redactor(app).Replace(string(res.Stderr)))
	if msg == "" {
		msg = "file command exited " + strconv.Itoa(res.ExitCode)
	}
	switch res.ExitCode {
	case 10:
		return nil, notFound(msg)
	case 11, 12:
		return nil, jobFieldError("path", msg)
	case 13, 14:
		return nil, &apiError{status: http.StatusConflict, code: dto.ErrorCodeConflict, msg: msg}
	}
	return nil, &apiError{status: http.StatusBadGateway, code: dto.ErrorCodeUnavailable, msg: msg}
}

var appFileTypes = map[string]dto.AppFileType{"f": dto.AppFileTypeFile, "d": dto.AppFileTypeDir, "l": dto.AppFileTypeSymlink}

func (s *Server) handleListAppFiles(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	rel, err := appFilePath(r.URL.Query().Get("path"), true)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out, err := s.appFilesExec(r.Context(), app, appFilesListScript, rel, []string{strconv.Itoa(appFileListMax + 1)},
		nil, 2<<20)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	list := dto.AppFileList{Path: rel, Entries: []dto.AppFileEntry{}}
	for rec := range bytes.SplitSeq(out, []byte{0}) {
		f := strings.SplitN(string(rec), "\t", 4)
		if len(f) != 4 {
			continue
		}
		if len(list.Entries) == appFileListMax {
			list.Truncated = true
			break
		}
		typ, ok := appFileTypes[f[0]]
		if !ok {
			typ = dto.AppFileTypeOther
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		e := dto.AppFileEntry{Name: f[3], Type: typ, Size: size}
		if sec, err := strconv.ParseFloat(f[2], 64); err == nil {
			e.ModifiedAt = time.Unix(int64(sec), 0).UTC().Format(time.RFC3339)
		}
		list.Entries = append(list.Entries, e)
	}
	slices.SortFunc(list.Entries, func(a, b dto.AppFileEntry) int {
		if (a.Type == dto.AppFileTypeDir) != (b.Type == dto.AppFileTypeDir) {
			if a.Type == dto.AppFileTypeDir {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	writeJSON(w, http.StatusOK, list)
}

// readAppFile reads a window of a file: its size, sha256 and raw bytes.
func (s *Server) readAppFile(ctx context.Context, app domain.App, rel string, offset int64, limit int) (int64, string, []byte, error) {
	out, err := s.appFilesExec(ctx, app, appFilesReadScript, rel,
		[]string{strconv.FormatInt(offset, 10), strconv.Itoa(limit)}, nil, limit+256)
	if err != nil {
		return 0, "", nil, err
	}
	head, data, ok := bytes.Cut(out, []byte("\n"))
	f := strings.Fields(string(head))
	if !ok || len(f) != 2 {
		return 0, "", nil, &apiError{status: http.StatusBadGateway, code: dto.ErrorCodeUnavailable, msg: "unreadable file command output"}
	}
	size, _ := strconv.ParseInt(f[0], 10, 64)
	return size, f[1], data, nil
}

// textOnly refuses binary data. A window may end inside a UTF-8 sequence;
// that partial rune is dropped so the next window starts on it.
func textOnly(data []byte, last bool) ([]byte, error) {
	if !last {
		for i := 0; i < utf8.UTFMax && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return nil, jobFieldError("path", "binary file: only UTF-8 text files can be read or edited here")
	}
	return data, nil
}

func (s *Server) handleReadAppFile(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	rel, err := appFilePath(r.URL.Query().Get("path"), false)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	limit, err := boundedInt(r, "limit", appFileReadDefault, appFileReadMax)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	var offset int64
	if v := r.URL.Query().Get("offset"); v != "" {
		if offset, err = strconv.ParseInt(v, 10, 64); err != nil || offset < 0 {
			writeError(w, s.Log, badRequest("offset must be a byte offset"))
			return
		}
	}
	size, sum, data, err := s.readAppFile(r.Context(), app, rel, offset, limit)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	last := offset+int64(len(data)) >= size
	if data, err = textOnly(data, last); err != nil {
		writeError(w, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.AppFileContent{
		Path: rel, Size: size, Sha256: sum, Offset: offset, Length: int64(len(data)),
		EOF: offset+int64(len(data)) >= size, Content: redactor(app).Replace(string(data)),
	})
}

// writeAppFile replaces (expected set) or creates (expected empty) a file.
func (s *Server) writeAppFile(ctx context.Context, app domain.App, rel, expected string, content []byte) (dto.AppFileWritten, error) {
	mode := "create"
	if expected != "" {
		mode = "replace"
	}
	out, err := s.appFilesExec(ctx, app, appFilesWriteScript, rel, []string{mode, expected}, bytes.NewReader(content), 4096)
	if err != nil {
		return dto.AppFileWritten{}, err
	}
	f := strings.Fields(string(out))
	if len(f) != 3 {
		return dto.AppFileWritten{}, &apiError{status: http.StatusBadGateway, code: dto.ErrorCodeUnavailable,
			msg: "unreadable file command output"}
	}
	size, _ := strconv.ParseInt(f[0], 10, 64)
	return dto.AppFileWritten{Path: rel, Size: size, Sha256: f[1], Created: f[2] == "1"}, nil
}

// redactedMarker in submitted text means the caller copied a redacted read:
// writing it would replace a real secret with the marker.
func refuseRedacted(field, text string) error {
	if strings.Contains(text, redactedValue) {
		return jobFieldError(field, "contains "+redactedValue+": secrets are hidden on read and cannot be written "+
			"back; use a replace that does not touch them")
	}
	return nil
}

func (s *Server) handleWriteAppFile(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.AppFileWriteRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	rel, err := appFilePath(req.Path, false)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	if req.ExpectedSha256 != "" && !sha256Pattern.MatchString(req.ExpectedSha256) {
		writeError(w, s.Log, jobFieldError("expectedSha256", "must be the sha256 from a read"))
		return
	}
	if !utf8.ValidString(req.Content) || strings.ContainsRune(req.Content, 0) {
		writeError(w, s.Log, jobFieldError("content", "must be UTF-8 text"))
		return
	}
	if err := refuseRedacted("content", req.Content); err != nil {
		writeError(w, s.Log, err)
		return
	}
	out, err := s.writeAppFile(r.Context(), app, rel, req.ExpectedSha256, []byte(req.Content))
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}

func (s *Server) handleReplaceAppFile(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req dto.AppFileReplaceRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, s.Log, err)
		return
	}
	rel, err := appFilePath(req.Path, false)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	if req.OldText == "" {
		writeError(w, s.Log, jobFieldError("oldText", "is required"))
		return
	}
	if req.ExpectedSha256 != "" && !sha256Pattern.MatchString(req.ExpectedSha256) {
		writeError(w, s.Log, jobFieldError("expectedSha256", "must be the sha256 from a read"))
		return
	}
	for field, v := range map[string]string{"oldText": req.OldText, "newText": req.NewText} {
		if err := refuseRedacted(field, v); err != nil {
			writeError(w, s.Log, err)
			return
		}
	}
	size, sum, data, err := s.readAppFile(r.Context(), app, rel, 0, appFileEditMax)
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	if size > appFileEditMax {
		writeError(w, s.Log, jobFieldError("path", "file is larger than 1 MiB"))
		return
	}
	if req.ExpectedSha256 != "" && req.ExpectedSha256 != sum {
		writeError(w, s.Log, &apiError{status: http.StatusConflict, code: dto.ErrorCodeConflict,
			msg: "file changed since it was read (sha256 now " + sum + "); read it again"})
		return
	}
	// The window and its hash come from two commands; check they agree.
	if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != sum {
		writeError(w, s.Log, &apiError{status: http.StatusConflict, code: dto.ErrorCodeConflict,
			msg: "file changed while it was read; try again"})
		return
	}
	if _, err := textOnly(data, true); err != nil {
		writeError(w, s.Log, err)
		return
	}
	text := string(data)
	n := strings.Count(text, req.OldText)
	switch {
	case n == 0:
		writeError(w, s.Log, jobFieldError("oldText", "not found in the file"))
		return
	case n > 1 && !req.All:
		writeError(w, s.Log, jobFieldError("oldText", strconv.Itoa(n)+" matches; add context to make it unique or set all"))
		return
	}
	if !req.All {
		n = 1
	}
	text = strings.Replace(text, req.OldText, req.NewText, n)
	out, err := s.writeAppFile(r.Context(), app, rel, sum, []byte(text))
	if err != nil {
		writeError(w, s.Log, err)
		return
	}
	out.Replacements = n
	writeJSON(w, http.StatusOK, out)
}
