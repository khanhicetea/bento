package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

// isUniqueViolation reports whether err is a SQLite UNIQUE or PRIMARY KEY
// constraint failure (both report "UNIQUE constraint failed").
func isUniqueViolation(err error) bool {
	se, ok := errors.AsType[*sqlite.Error](err)
	if !ok {
		return false
	}
	switch se.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return true
	}
	return false
}

func now() string { return platform.FormatTime(time.Now()) }

// ---- meta and settings ----

func GetMeta(ctx context.Context, q Q, key string) (string, error) {
	var v string
	err := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func SetMeta(ctx context.Context, q Q, key, value string) error {
	_, err := q.ExecContext(ctx, "INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// GetSetting decodes a JSON setting into out; missing settings leave out
// unchanged and return false.
func GetSetting(ctx context.Context, q Q, key string, out any) (bool, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), out)
}

func PutSetting(ctx context.Context, q Q, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO settings(key, value_json, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`, key, string(raw), now())
	return err
}

// StackIdentity is the stable stack identity stored in meta.
type StackIdentity struct {
	ID        string
	Name      string
	CreatedAt string
}

func GetStackIdentity(ctx context.Context, q Q) (StackIdentity, error) {
	var s StackIdentity
	var err error
	if s.ID, err = GetMeta(ctx, q, "stack_id"); err != nil {
		return s, err
	}
	if s.Name, err = GetMeta(ctx, q, "stack_name"); err != nil {
		return s, err
	}
	s.CreatedAt, _ = GetMeta(ctx, q, "created_at")
	return s, nil
}

// ---- UID allocation ledger ----

// ErrUIDExhausted is returned when the configured range has no unused IDs.
var ErrUIDExhausted = errors.New("uid range exhausted")

// AllocateUID reserves the next never-used UID (== GID) above the persisted
// high-water mark, skipping host collisions. The high-water mark never
// decreases and ledger rows are never deleted, so IDs are not reused within
// this stack lineage.
func AllocateUID(ctx context.Context, q Q, rng domain.UIDRange, host platform.HostIDs, appID, slug string) (int, error) {
	hw := rng.First - 1
	if v, err := GetMeta(ctx, q, "uid_highwater"); err == nil {
		n, perr := strconv.Atoi(v)
		if perr != nil {
			return 0, fmt.Errorf("corrupt uid_highwater %q", v)
		}
		if n > hw {
			hw = n
		}
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	var ledgerMax sql.NullInt64
	if err := q.QueryRowContext(ctx, "SELECT MAX(uid) FROM uid_ledger").Scan(&ledgerMax); err != nil {
		return 0, err
	}
	if ledgerMax.Valid && int(ledgerMax.Int64) > hw {
		hw = int(ledgerMax.Int64)
	}
	for candidate := hw + 1; candidate <= rng.Last; candidate++ {
		if host != nil && host.Taken(candidate) {
			// Skipped IDs still advance the high-water mark.
			continue
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO uid_ledger(uid, app_id, slug, state, allocated_at) VALUES(?, ?, ?, 'allocated', ?)",
			candidate, appID, slug, now()); err != nil {
			return 0, err
		}
		if err := SetMeta(ctx, q, "uid_highwater", strconv.Itoa(candidate)); err != nil {
			return 0, err
		}
		return candidate, nil
	}
	return 0, fmt.Errorf("%w: no unused uid in %d-%d", ErrUIDExhausted, rng.First, rng.Last)
}

func SetLedgerState(ctx context.Context, q Q, uid int, state string) error {
	retired := sql.NullString{}
	if state == "retired" || state == "burned" {
		retired = sql.NullString{String: now(), Valid: true}
	}
	_, err := q.ExecContext(ctx, "UPDATE uid_ledger SET state = ?, retired_at = COALESCE(?, retired_at) WHERE uid = ?", state, retired, uid)
	return err
}

type LedgerEntry struct {
	UID         int
	AppID       string
	Slug        string
	State       string
	AllocatedAt string
	RetiredAt   string
}

func ListLedger(ctx context.Context, q Q) ([]LedgerEntry, error) {
	rows, err := q.QueryContext(ctx, "SELECT uid, app_id, slug, state, allocated_at, COALESCE(retired_at,'') FROM uid_ledger ORDER BY uid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.UID, &e.AppID, &e.Slug, &e.State, &e.AllocatedAt, &e.RetiredAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- apps ----

const appColumns = `id, slug, uid, gid, runtime_json, resources_json, desired_runtime, ingress, publication,
	route_json, redis_json, config_generation, credentials_generation, provisioned, created_at, updated_at`

func scanApp(row interface{ Scan(...any) error }) (domain.App, error) {
	var a domain.App
	var runtimeJSON, resourcesJSON, routeJSON, redisJSON, created, updated string
	var provisioned int
	err := row.Scan(&a.ID, &a.Slug, &a.UID, &a.GID, &runtimeJSON, &resourcesJSON, &a.DesiredRuntime, &a.Ingress,
		&a.Publication, &routeJSON, &redisJSON, &a.ConfigGeneration, &a.CredentialsGeneration, &provisioned, &created, &updated)
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal([]byte(runtimeJSON), &a.Runtime); err != nil {
		return a, fmt.Errorf("app %s runtime: %w", a.Slug, err)
	}
	if err := json.Unmarshal([]byte(resourcesJSON), &a.Resources); err != nil {
		return a, err
	}
	if err := json.Unmarshal([]byte(routeJSON), &a.Route); err != nil {
		return a, err
	}
	if err := json.Unmarshal([]byte(redisJSON), &a.Redis); err != nil {
		return a, err
	}
	a.Provisioned = provisioned == 1
	a.CreatedAt = platform.ParseTime(created)
	a.UpdatedAt = platform.ParseTime(updated)
	return a, nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func InsertApp(ctx context.Context, q Q, a domain.App) error {
	provisioned := 0
	if a.Provisioned {
		provisioned = 1
	}
	_, err := q.ExecContext(ctx, `INSERT INTO apps(`+appColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Slug, a.UID, a.GID, mustJSON(a.Runtime), mustJSON(a.Resources), a.DesiredRuntime, a.Ingress,
		a.Publication, mustJSON(a.Route), mustJSON(a.Redis), a.ConfigGeneration, a.CredentialsGeneration, provisioned,
		platform.FormatTime(a.CreatedAt), platform.FormatTime(a.UpdatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: app slug or uid already exists", ErrConflict)
	}
	return err
}

// UpdateApp persists mutable desired state. Identity columns are never updated.
func UpdateApp(ctx context.Context, q Q, a domain.App) error {
	provisioned := 0
	if a.Provisioned {
		provisioned = 1
	}
	res, err := q.ExecContext(ctx, `UPDATE apps SET runtime_json=?, resources_json=?, desired_runtime=?, ingress=?, publication=?,
		route_json=?, redis_json=?, config_generation=?, credentials_generation=?, provisioned=?, updated_at=? WHERE id=?`,
		mustJSON(a.Runtime), mustJSON(a.Resources), a.DesiredRuntime, a.Ingress, a.Publication, mustJSON(a.Route),
		mustJSON(a.Redis), a.ConfigGeneration, a.CredentialsGeneration, provisioned, now(), a.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteApp(ctx context.Context, q Q, id string) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM domains WHERE owner_kind='app' AND owner_id=?", id); err != nil {
		return err
	}
	// The deploy key and webhook secret die with the app.
	if _, err := q.ExecContext(ctx, "DELETE FROM settings WHERE key IN (?, ?)", gitSourceKey(id), webhookKey(id)); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, "DELETE FROM apps WHERE id=?", id)
	return err
}

// GetApp loads an app by id or slug, including bindings and domains.
func GetApp(ctx context.Context, q Q, idOrSlug string) (domain.App, error) {
	a, err := scanApp(q.QueryRowContext(ctx, "SELECT "+appColumns+" FROM apps WHERE id = ? OR slug = ?", idOrSlug, idOrSlug))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	return a, loadAppRelations(ctx, q, &a)
}

func ListApps(ctx context.Context, q Q) ([]domain.App, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+appColumns+" FROM apps ORDER BY slug")
	if err != nil {
		return nil, err
	}
	var apps []domain.App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		apps = append(apps, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range apps {
		if err := loadAppRelations(ctx, q, &apps[i]); err != nil {
			return nil, err
		}
	}
	return apps, nil
}

func loadAppRelations(ctx context.Context, q Q, a *domain.App) error {
	var err error
	if a.Bindings, err = ListBindings(ctx, q, a.ID); err != nil {
		return err
	}
	a.Domains, err = ListDomains(ctx, q, "app", a.ID)
	return err
}

// ---- bindings ----

func InsertBinding(ctx context.Context, q Q, b domain.Binding) error {
	var vacuum sql.NullString
	if b.Vacuum != nil {
		vacuum = sql.NullString{String: mustJSON(b.Vacuum), Valid: true}
	}
	var pos int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM bindings WHERE app_id = ?", b.AppID).Scan(&pos); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO bindings(id, app_id, engine, service, username, password, sqlite_file_id, vacuum_json, position, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, b.ID, b.AppID, b.Engine, nullable(b.Service), nullable(b.Username), nullable(b.Password),
		nullable(b.SQLiteFileID), vacuum, pos, platform.FormatTime(b.CreatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: the app already has a binding for this service", ErrConflict)
	}
	return err
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func ListBindings(ctx context.Context, q Q, appID string) ([]domain.Binding, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, app_id, engine, COALESCE(service,''), COALESCE(username,''), COALESCE(password,''),
		COALESCE(sqlite_file_id,''), COALESCE(vacuum_json,''), created_at FROM bindings WHERE app_id = ? ORDER BY position`, appID)
	if err != nil {
		return nil, err
	}
	var out []domain.Binding
	for rows.Next() {
		var b domain.Binding
		var vacuum, created string
		if err := rows.Scan(&b.ID, &b.AppID, &b.Engine, &b.Service, &b.Username, &b.Password, &b.SQLiteFileID, &vacuum, &created); err != nil {
			rows.Close()
			return nil, err
		}
		if vacuum != "" {
			b.Vacuum = &domain.VacuumSlot{}
			if err := json.Unmarshal([]byte(vacuum), b.Vacuum); err != nil {
				rows.Close()
				return nil, err
			}
		}
		b.CreatedAt = platform.ParseTime(created)
		out = append(out, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		names, err := q.QueryContext(ctx, "SELECT name FROM binding_databases WHERE binding_id = ? ORDER BY created_at, name", out[i].ID)
		if err != nil {
			return nil, err
		}
		for names.Next() {
			var n string
			if err := names.Scan(&n); err != nil {
				names.Close()
				return nil, err
			}
			out[i].Databases = append(out[i].Databases, n)
		}
		names.Close()
	}
	return out, nil
}

func AddBindingDatabase(ctx context.Context, q Q, bindingID, name string) error {
	_, err := q.ExecContext(ctx, "INSERT INTO binding_databases(binding_id, name, created_at) VALUES(?,?,?)", bindingID, name, now())
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: database %s already exists on this binding", ErrConflict, name)
	}
	return err
}

// ---- domains ----

func ListDomains(ctx context.Context, q Q, ownerKind, ownerID string) ([]domain.DomainLink, error) {
	rows, err := q.QueryContext(ctx, "SELECT name, is_primary FROM domains WHERE owner_kind = ? AND owner_id = ? ORDER BY is_primary DESC, name", ownerKind, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DomainLink
	for rows.Next() {
		var d domain.DomainLink
		var p int
		if err := rows.Scan(&d.Name, &p); err != nil {
			return nil, err
		}
		d.Primary = p == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

type DomainOwner struct {
	Name      string
	OwnerKind string
	OwnerID   string
	Primary   bool
}

func DomainOwnerOf(ctx context.Context, q Q, name string) (DomainOwner, error) {
	var d DomainOwner
	var p int
	err := q.QueryRowContext(ctx, "SELECT name, owner_kind, owner_id, is_primary FROM domains WHERE name = ?", name).Scan(&d.Name, &d.OwnerKind, &d.OwnerID, &p)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	d.Primary = p == 1
	return d, err
}

// ReplaceDomains sets an owner's domain links. Every name must be unowned or
// already owned by this owner; exactly one link is primary.
func ReplaceDomains(ctx context.Context, q Q, ownerKind, ownerID string, links []domain.DomainLink) error {
	primaries := 0
	for _, l := range links {
		if l.Primary {
			primaries++
		}
		owner, err := DomainOwnerOf(ctx, q, l.Name)
		if err == nil && (owner.OwnerKind != ownerKind || owner.OwnerID != ownerID) {
			return fmt.Errorf("%w: domain %s is already claimed by another %s", ErrConflict, l.Name, owner.OwnerKind)
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if len(links) > 0 && primaries != 1 {
		return errors.New("exactly one primary domain is required")
	}
	if _, err := q.ExecContext(ctx, "DELETE FROM domains WHERE owner_kind = ? AND owner_id = ?", ownerKind, ownerID); err != nil {
		return err
	}
	for _, l := range links {
		p := 0
		if l.Primary {
			p = 1
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO domains(name, owner_kind, owner_id, is_primary, created_at) VALUES(?,?,?,?,?)",
			l.Name, ownerKind, ownerID, p, now()); err != nil {
			return err
		}
	}
	return nil
}

// ---- proxies ----

func scanProxy(row interface{ Scan(...any) error }) (domain.Proxy, error) {
	var p domain.Proxy
	var ups, route, created, updated string
	var enabled int
	if err := row.Scan(&p.ID, &p.Name, &ups, &route, &enabled, &created, &updated); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(ups), &p.Upstreams); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(route), &p.Route); err != nil {
		return p, err
	}
	p.Enabled = enabled == 1
	p.CreatedAt = platform.ParseTime(created)
	p.UpdatedAt = platform.ParseTime(updated)
	return p, nil
}

func ListProxies(ctx context.Context, q Q) ([]domain.Proxy, error) {
	rows, err := q.QueryContext(ctx, "SELECT id, name, upstreams_json, route_json, enabled, created_at, updated_at FROM proxies ORDER BY name")
	if err != nil {
		return nil, err
	}
	var out []domain.Proxy
	for rows.Next() {
		p, err := scanProxy(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	for i := range out {
		if out[i].Domains, err = ListDomains(ctx, q, "proxy", out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func GetProxy(ctx context.Context, q Q, idOrName string) (domain.Proxy, error) {
	p, err := scanProxy(q.QueryRowContext(ctx, "SELECT id, name, upstreams_json, route_json, enabled, created_at, updated_at FROM proxies WHERE id = ? OR name = ?", idOrName, idOrName))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Domains, err = ListDomains(ctx, q, "proxy", p.ID)
	return p, err
}

func UpsertProxy(ctx context.Context, q Q, p domain.Proxy) error {
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	_, err := q.ExecContext(ctx, `INSERT INTO proxies(id, name, upstreams_json, route_json, enabled, created_at, updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET upstreams_json=excluded.upstreams_json, route_json=excluded.route_json, enabled=excluded.enabled, updated_at=excluded.updated_at`,
		p.ID, p.Name, mustJSON(p.Upstreams), mustJSON(p.Route), enabled, platform.FormatTime(p.CreatedAt), now())
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: proxy name already exists", ErrConflict)
	}
	return err
}

func DeleteProxy(ctx context.Context, q Q, id string) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM domains WHERE owner_kind='proxy' AND owner_id=?", id); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, "DELETE FROM proxies WHERE id=?", id)
	return err
}

// ---- data services ----

type ServiceRow struct {
	domain.DataService
	Initialized bool
}

func ListServices(ctx context.Context, q Q) ([]ServiceRow, error) {
	rows, err := q.QueryContext(ctx, "SELECT name, engine, version, image, volume, initialized, created_at FROM data_services ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceRow
	for rows.Next() {
		var s ServiceRow
		var init int
		var created string
		if err := rows.Scan(&s.Name, &s.Engine, &s.Version, &s.Image, &s.Volume, &init, &created); err != nil {
			return nil, err
		}
		s.Initialized = init == 1
		s.CreatedAt = platform.ParseTime(created)
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetService(ctx context.Context, q Q, name string) (ServiceRow, error) {
	var s ServiceRow
	var init int
	var created string
	err := q.QueryRowContext(ctx, "SELECT name, engine, version, image, volume, initialized, created_at FROM data_services WHERE name = ?", name).
		Scan(&s.Name, &s.Engine, &s.Version, &s.Image, &s.Volume, &init, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	s.Initialized = init == 1
	s.CreatedAt = platform.ParseTime(created)
	return s, err
}

func InsertService(ctx context.Context, q Q, s domain.DataService) error {
	_, err := q.ExecContext(ctx, "INSERT INTO data_services(name, engine, version, image, volume, created_at) VALUES(?,?,?,?,?,?)",
		s.Name, s.Engine, s.Version, s.Image, s.Volume, platform.FormatTime(s.CreatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: service %s already exists", ErrConflict, s.Name)
	}
	return err
}

func MarkServiceInitialized(ctx context.Context, q Q, name string) error {
	_, err := q.ExecContext(ctx, "UPDATE data_services SET initialized = 1 WHERE name = ?", name)
	return err
}

// ---- images ----

type ImageRecord struct {
	Key         string
	Tag         string
	ImageID     string
	ContextHash string
	BuiltAt     string
}

func GetImage(ctx context.Context, q Q, key string) (ImageRecord, error) {
	var r ImageRecord
	err := q.QueryRowContext(ctx, "SELECT key, tag, image_id, context_hash, built_at FROM images WHERE key = ?", key).
		Scan(&r.Key, &r.Tag, &r.ImageID, &r.ContextHash, &r.BuiltAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func PutImage(ctx context.Context, q Q, r ImageRecord) error {
	_, err := q.ExecContext(ctx, `INSERT INTO images(key, tag, image_id, context_hash, built_at) VALUES(?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET tag=excluded.tag, image_id=excluded.image_id, context_hash=excluded.context_hash, built_at=excluded.built_at`,
		r.Key, r.Tag, r.ImageID, r.ContextHash, now())
	return err
}

func ListImages(ctx context.Context, q Q) ([]ImageRecord, error) {
	rows, err := q.QueryContext(ctx, "SELECT key, tag, image_id, context_hash, built_at FROM images ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageRecord
	for rows.Next() {
		var r ImageRecord
		if err := rows.Scan(&r.Key, &r.Tag, &r.ImageID, &r.ContextHash, &r.BuiltAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- retired incarnations ----

type RetainedRelational struct {
	Engine    domain.Engine `json:"engine"`
	Service   string        `json:"service"`
	Username  string        `json:"username"`
	Databases []string      `json:"databases"`
}

type RetainedArtifacts struct {
	Home          string               `json:"home"`
	SQLiteFileIDs []string             `json:"sqliteFileIds"`
	Relational    []RetainedRelational `json:"relational"`
	RedisUser     string               `json:"redisUser"`
}

type RetiredApp struct {
	AppID     string
	Slug      string
	UID       int
	RetiredAt string
	Artifacts RetainedArtifacts
	PrunedAt  string
}

func InsertRetired(ctx context.Context, q Q, r RetiredApp) error {
	_, err := q.ExecContext(ctx, "INSERT INTO retired_apps(app_id, slug, uid, retired_at, artifacts_json) VALUES(?,?,?,?,?)",
		r.AppID, r.Slug, r.UID, now(), mustJSON(r.Artifacts))
	return err
}

func ListRetired(ctx context.Context, q Q) ([]RetiredApp, error) {
	rows, err := q.QueryContext(ctx, "SELECT app_id, slug, uid, retired_at, artifacts_json, COALESCE(pruned_at,'') FROM retired_apps ORDER BY retired_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetiredApp
	for rows.Next() {
		var r RetiredApp
		var arts string
		if err := rows.Scan(&r.AppID, &r.Slug, &r.UID, &r.RetiredAt, &arts, &r.PrunedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(arts), &r.Artifacts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func GetRetired(ctx context.Context, q Q, appID string) (RetiredApp, error) {
	all, err := ListRetired(ctx, q)
	if err != nil {
		return RetiredApp{}, err
	}
	for _, r := range all {
		if r.AppID == appID {
			return r, nil
		}
	}
	return RetiredApp{}, ErrNotFound
}

func MarkPruned(ctx context.Context, q Q, appID string) error {
	_, err := q.ExecContext(ctx, "UPDATE retired_apps SET pruned_at=? WHERE app_id=?", now(), appID)
	return err
}

func DeleteSetting(ctx context.Context, q Q, key string) error {
	_, err := q.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key)
	return err
}

func gitSourceKey(appID string) string { return "git_source:" + appID }

// GetGitSource returns the app's repository source; ok is false when none is
// configured.
func GetGitSource(ctx context.Context, q Q, appID string) (domain.GitSource, bool, error) {
	var g domain.GitSource
	ok, err := GetSetting(ctx, q, gitSourceKey(appID), &g)
	return g, ok, err
}

func PutGitSource(ctx context.Context, q Q, appID string, g domain.GitSource) error {
	return PutSetting(ctx, q, gitSourceKey(appID), g)
}

func DeleteGitSource(ctx context.Context, q Q, appID string) error {
	return DeleteSetting(ctx, q, gitSourceKey(appID))
}

func webhookKey(appID string) string { return "webhook:" + appID }

// GetWebhook returns the app's deploy webhook; ok is false when none is
// enabled.
func GetWebhook(ctx context.Context, q Q, appID string) (domain.Webhook, bool, error) {
	var w domain.Webhook
	ok, err := GetSetting(ctx, q, webhookKey(appID), &w)
	return w, ok, err
}

func PutWebhook(ctx context.Context, q Q, appID string, w domain.Webhook) error {
	return PutSetting(ctx, q, webhookKey(appID), w)
}

func DeleteWebhook(ctx context.Context, q Q, appID string) error {
	return DeleteSetting(ctx, q, webhookKey(appID))
}

// FindWebhook resolves a hook id to its app id and webhook.
func FindWebhook(ctx context.Context, q Q, hookID string) (string, domain.Webhook, error) {
	rows, err := q.QueryContext(ctx, "SELECT key, value_json FROM settings WHERE key LIKE 'webhook:%'")
	if err != nil {
		return "", domain.Webhook{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return "", domain.Webhook{}, err
		}
		var w domain.Webhook
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return "", domain.Webhook{}, err
		}
		if w.HookID != "" && w.HookID == hookID {
			return strings.TrimPrefix(key, "webhook:"), w, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", domain.Webhook{}, err
	}
	return "", domain.Webhook{}, ErrNotFound
}
