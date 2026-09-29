// Package dataservices manages the shared MySQL, PostgreSQL, and Redis
// services: container specs, administrator secrets, readiness, and
// add-only app grants. Administrator and app secrets never appear on host
// argv, in labels, or in API output; SQL and ACLs travel over exec stdin or
// private files.
package dataservices

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/runtime"
)

// Manager operates the stack's data services.
type Manager struct {
	Engine docker.Engine
	Layout platform.Layout
	Names  runtime.Names
}

func (m *Manager) secretsDir(service string) string {
	return filepath.Join(m.Layout.ServicesDir(), service, "secrets")
}

func (m *Manager) confDir(service string) string {
	return filepath.Join(m.Layout.ServicesDir(), service, "conf")
}

// EnsureSecrets generates administrator secrets once. Existing secrets are
// never rotated.
func (m *Manager) EnsureSecrets(s domain.DataService) error {
	dir := m.secretsDir(s.Name)
	if err := platform.EnsureDir(filepath.Dir(dir), 0o700, platform.RootOwner); err != nil {
		return err
	}
	if err := platform.EnsureDir(dir, 0o700, platform.RootOwner); err != nil {
		return err
	}
	pwPath := filepath.Join(dir, "root-password")
	pw, err := os.ReadFile(pwPath)
	if os.IsNotExist(err) {
		pw = []byte(platform.RandomPassword(32))
		if err := platform.AtomicWrite(pwPath, pw, 0o400, platform.RootOwner); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	password := strings.TrimSpace(string(pw))
	switch s.Engine {
	case domain.EngineMySQL:
		cnf := fmt.Sprintf("[client]\nuser=root\npassword=%s\nhost=localhost\n", password)
		if _, err := platform.WriteIfChanged(filepath.Join(dir, "client.cnf"), []byte(cnf), 0o400, platform.RootOwner); err != nil {
			return err
		}
	case domain.EngineRedis:
		if err := platform.EnsureDir(filepath.Dir(m.confDir(s.Name)), 0o700, platform.RootOwner); err != nil {
			return err
		}
	}
	return nil
}

// AdminPassword reads a service administrator secret (backend-internal only).
func (m *Manager) AdminPassword(service string) (string, error) {
	b, err := os.ReadFile(filepath.Join(m.secretsDir(service), "root-password"))
	return strings.TrimSpace(string(b)), err
}

func (m *Manager) volumeTarget(s domain.DataService) string {
	switch s.Engine {
	case domain.EngineMySQL:
		return "/var/lib/mysql"
	case domain.EnginePostgres:
		// PostgreSQL 18+ images keep PGDATA in a major-versioned subdirectory
		// and refuse a volume mounted at the legacy /var/lib/postgresql/data.
		if major, err := strconv.Atoi(s.Version); err == nil && major >= 18 {
			return "/var/lib/postgresql"
		}
		return "/var/lib/postgresql/data"
	default:
		return "/data"
	}
}

func roleOf(e domain.Engine) runtime.Role {
	if e == domain.EngineRedis {
		return runtime.RoleCache
	}
	return runtime.RoleDatabase
}

// ContainerSpec plans the service container. Data services join only the
// private data network and publish no host ports.
func (m *Manager) ContainerSpec(s domain.DataService) docker.ContainerSpec {
	labels := m.Names.Labels(roleOf(s.Engine), map[string]string{runtime.LabelService: s.Name})
	mounts := []mount.Mount{
		{Type: mount.TypeVolume, Source: s.Volume, Target: m.volumeTarget(s)},
		{Type: mount.TypeBind, Source: m.secretsDir(s.Name), Target: "/run/bento-secrets", ReadOnly: true},
	}
	cfg := &container.Config{Image: s.Image, Labels: labels, Hostname: s.Name}
	switch s.Engine {
	case domain.EngineMySQL:
		cfg.Env = []string{"MYSQL_ROOT_PASSWORD_FILE=/run/bento-secrets/root-password"}
		cfg.Healthcheck = &container.HealthConfig{
			Test:     []string{"CMD", "mysqladmin", "--defaults-extra-file=/run/bento-secrets/client.cnf", "ping", "--silent"},
			Interval: 15 * time.Second, Timeout: 5 * time.Second, StartPeriod: 120 * time.Second, Retries: 5,
		}
	case domain.EnginePostgres:
		cfg.Env = []string{"POSTGRES_PASSWORD_FILE=/run/bento-secrets/root-password"}
		cfg.Healthcheck = &container.HealthConfig{
			Test:     []string{"CMD", "pg_isready", "-U", "postgres"},
			Interval: 15 * time.Second, Timeout: 5 * time.Second, StartPeriod: 60 * time.Second, Retries: 5,
		}
	case domain.EngineRedis:
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: m.confDir(s.Name), Target: "/etc/redis-bento", ReadOnly: true})
		cfg.Cmd = []string{"redis-server", "/etc/redis-bento/redis.conf"}
		cfg.User = "999:999"
		// Liveness only: an unauthenticated PING proves the server answers.
		cfg.Healthcheck = &container.HealthConfig{
			Test:     []string{"CMD-SHELL", "redis-cli ping 2>&1 | grep -qE 'PONG|NOAUTH'"},
			Interval: 15 * time.Second, Timeout: 5 * time.Second, StartPeriod: 20 * time.Second, Retries: 5,
		}
	}
	stop := 60
	cfg.StopTimeout = &stop
	host := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Mounts:        mounts,
		SecurityOpt:   []string{"no-new-privileges:true"},
		LogConfig:     container.LogConfig{Type: "local", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		ShmSize:       128 << 20,
	}
	if s.Engine == domain.EngineRedis {
		host.ReadonlyRootfs = true
		host.CapDrop = []string{"ALL"}
	}
	return docker.ContainerSpec{
		Name: m.Names.ServiceContainer(s.Name), Config: cfg, HostConfig: host,
		Networking: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			m.Names.DataNetwork(): {Aliases: []string{s.Name}},
		}},
	}
}

// Ready runs the engine-specific readiness command inside the container.
func (m *Manager) Ready(ctx context.Context, s domain.DataService, containerID string) error {
	var req docker.ExecRequest
	switch s.Engine {
	// First-boot entrypoints run a temporary socket-only server; requiring a
	// TCP answer ensures the real server is up.
	case domain.EngineMySQL:
		req.Cmd = []string{"mysqladmin", "--defaults-extra-file=/run/bento-secrets/client.cnf", "--protocol=tcp", "-h", "127.0.0.1", "ping", "--silent"}
	case domain.EnginePostgres:
		req.Cmd = []string{"pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-q"}
	case domain.EngineRedis:
		pw, err := m.AdminPassword(s.Name)
		if err != nil {
			return err
		}
		req.Cmd = []string{"redis-cli", "--no-auth-warning", "PING"}
		req.Env = []string{"REDISCLI_AUTH=" + pw}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := m.Engine.Exec(cctx, containerID, req)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s not ready (exit %d)", s.Name, res.ExitCode)
	}
	return nil
}

// ---- relational administration ----

// SQL runs administrative SQL over stdin (never argv).
func (m *Manager) SQL(ctx context.Context, s domain.DataService, containerID, database, sql string) (string, error) {
	req := docker.ExecRequest{Stdin: strings.NewReader(sql), OutputLimit: 1 << 20}
	switch s.Engine {
	case domain.EngineMySQL:
		req.Cmd = []string{"mysql", "--defaults-extra-file=/run/bento-secrets/client.cnf", "-N", "-B"}
		if database != "" {
			req.Cmd = append(req.Cmd, database)
		}
	case domain.EnginePostgres:
		if database == "" {
			database = "postgres"
		}
		req.Cmd = []string{"psql", "-U", "postgres", "-d", database, "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1"}
	default:
		return "", fmt.Errorf("not a relational service")
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := m.Engine.Exec(cctx, containerID, req)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s admin SQL failed: %s", s.Name, redactLine(string(res.Stderr)))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

var identPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func ident(s string) (string, error) {
	if !identPattern.MatchString(s) {
		return "", fmt.Errorf("invalid identifier %q", s)
	}
	return s, nil
}

func redactLine(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

// ProvisionBinding creates the app user (idempotent) and each database,
// refusing to adopt a database that exists without this user's ownership.
func (m *Manager) ProvisionBinding(ctx context.Context, s domain.DataService, containerID string, b domain.Binding) error {
	user, err := ident(b.Username)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]+$`).MatchString(b.Password) {
		return fmt.Errorf("generated password has an unexpected format")
	}
	switch s.Engine {
	case domain.EngineMySQL:
		if _, err := m.SQL(ctx, s, containerID, "", fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s';\n", user, b.Password)); err != nil {
			return err
		}
		for _, name := range b.Databases {
			if err := m.mysqlDatabase(ctx, s, containerID, user, name); err != nil {
				return err
			}
		}
	case domain.EnginePostgres:
		role := fmt.Sprintf(`DO $$ BEGIN
IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN
  CREATE ROLE "%s" LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
END IF;
END $$;
`, user, user, b.Password)
		if _, err := m.SQL(ctx, s, containerID, "", role); err != nil {
			return err
		}
		for _, name := range b.Databases {
			if err := m.postgresDatabase(ctx, s, containerID, user, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) mysqlDatabase(ctx context.Context, s domain.DataService, id, user, name string) error {
	db, err := ident(name)
	if err != nil {
		return err
	}
	exists, err := m.SQL(ctx, s, id, "", fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '%s';\n", db))
	if err != nil {
		return err
	}
	if exists != "0" {
		grants, err := m.SQL(ctx, s, id, "", fmt.Sprintf("SELECT COUNT(*) FROM mysql.db WHERE User = '%s' AND Db = '%s';\n", user, db))
		if err != nil {
			return err
		}
		if grants == "0" {
			return &AdoptionRefused{Database: db}
		}
	}
	_, err = m.SQL(ctx, s, id, "", fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET %s COLLATE %s;\nGRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\n",
		db, domain.MySQLDefaultCharset, domain.MySQLDefaultCollation, db, user))
	return err
}

func (m *Manager) postgresDatabase(ctx context.Context, s domain.DataService, id, user, name string) error {
	db, err := ident(name)
	if err != nil {
		return err
	}
	owner, err := m.SQL(ctx, s, id, "", fmt.Sprintf("SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = '%s';\n", db))
	if err != nil {
		return err
	}
	if owner != "" && owner != user {
		return &AdoptionRefused{Database: db}
	}
	if owner == "" {
		if _, err := m.SQL(ctx, s, id, "", fmt.Sprintf("CREATE DATABASE \"%s\" OWNER \"%s\";\n", db, user)); err != nil {
			return err
		}
	}
	_, err = m.SQL(ctx, s, id, db, fmt.Sprintf(
		"REVOKE ALL ON DATABASE \"%s\" FROM PUBLIC;\nGRANT CONNECT, TEMPORARY ON DATABASE \"%s\" TO \"%s\";\nREVOKE ALL ON SCHEMA public FROM PUBLIC;\nALTER SCHEMA public OWNER TO \"%s\";\n",
		db, db, user, user))
	return err
}

// AdoptionRefused reports a database that exists without this binding's
// ownership, for example one retained from an earlier app incarnation.
type AdoptionRefused struct{ Database string }

func (e *AdoptionRefused) Error() string {
	return fmt.Sprintf("database %s already exists and is not owned by this binding; refusing to adopt it", e.Database)
}

// DropBinding permanently drops an app's databases and user (prune only).
func (m *Manager) DropBinding(ctx context.Context, s domain.DataService, id, user string, databases []string) error {
	u, err := ident(user)
	if err != nil {
		return err
	}
	var sql strings.Builder
	for _, name := range databases {
		db, err := ident(name)
		if err != nil {
			return err
		}
		if s.Engine == domain.EngineMySQL {
			fmt.Fprintf(&sql, "DROP DATABASE IF EXISTS `%s`;\n", db)
		} else {
			fmt.Fprintf(&sql, "DROP DATABASE IF EXISTS \"%s\" WITH (FORCE);\n", db)
		}
	}
	if s.Engine == domain.EngineMySQL {
		fmt.Fprintf(&sql, "DROP USER IF EXISTS '%s'@'%%';\n", u)
	} else {
		fmt.Fprintf(&sql, "DROP ROLE IF EXISTS \"%s\";\n", u)
	}
	_, err = m.SQL(ctx, s, id, "", sql.String())
	return err
}

// ---- Redis ACL ----

// RedisUser is one ACL identity restricted to its key/channel prefix.
type RedisUser struct {
	Username string
	Password string
	Prefix   string
}

// WriteRedisConfig renders redis.conf and the ACL file from the full set of
// app identities. The default user is the administrator.
func (m *Manager) WriteRedisConfig(service string, users []RedisUser) error {
	dir := m.confDir(service)
	if err := platform.EnsureDir(dir, 0o750, platform.Owner{UID: 0, GID: 999}); err != nil {
		return err
	}
	admin, err := m.AdminPassword(service)
	if err != nil {
		return err
	}
	conf := "# Generated by Bento. Do not edit.\n" +
		"port 6379\nbind 0.0.0.0\nprotected-mode yes\ndir /data\nappendonly yes\n" +
		"aclfile /etc/redis-bento/users.acl\n" +
		fmt.Sprintf("maxmemory %d\nmaxmemory-policy noeviction\n", RedisMaxMemory())
	slices.SortFunc(users, func(a, b RedisUser) int { return strings.Compare(a.Username, b.Username) })
	var acl bytes.Buffer
	fmt.Fprintf(&acl, "user default on sanitize-payload #%s ~* &* +@all\n", platform.SHA256Hex([]byte(admin)))
	for _, u := range users {
		if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(u.Username) || !regexp.MustCompile(`^[a-z0-9-]+:$`).MatchString(u.Prefix) {
			return fmt.Errorf("invalid redis identity %q", u.Username)
		}
		// INFO is read-only and called on connect by many clients (BullMQ,
		// Sidekiq, health checks); the rest of @dangerous stays denied because
		// FLUSHDB, KEYS and friends ignore key prefixes.
		fmt.Fprintf(&acl, "user %s on sanitize-payload #%s resetkeys ~%s* resetchannels &%s* +@all -@admin -@dangerous +info\n",
			u.Username, platform.SHA256Hex([]byte(u.Password)), u.Prefix, u.Prefix)
	}
	owner := platform.Owner{UID: 0, GID: 999}
	if _, err := platform.WriteIfChanged(filepath.Join(dir, "redis.conf"), []byte(conf), 0o440, owner); err != nil {
		return err
	}
	_, err = platform.WriteIfChanged(filepath.Join(dir, "users.acl"), acl.Bytes(), 0o440, owner)
	return err
}

// RedisMaxMemory bounds the shared Redis at a quarter of host memory so one
// app cannot grow it until the host runs out; 256 MiB when unknown.
func RedisMaxMemory() int64 {
	const floor = 64 << 20
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 256 << 20
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				break
			}
			return max(kb*1024/4, floor)
		}
	}
	return 256 << 20
}

// ReloadRedis makes the running server load its ACL file and memory bound.
// It is cheap and idempotent, so callers run it on every sync rather than
// only when the files changed: a failed reload is then retried next time.
func (m *Manager) ReloadRedis(ctx context.Context, service, containerID string) error {
	pw, err := m.AdminPassword(service)
	if err != nil {
		return err
	}
	for _, cmd := range [][]string{
		{"redis-cli", "--no-auth-warning", "ACL", "LOAD"},
		{"redis-cli", "--no-auth-warning", "CONFIG", "SET", "maxmemory", strconv.FormatInt(RedisMaxMemory(), 10)},
	} {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		res, err := m.Engine.Exec(cctx, containerID, docker.ExecRequest{Cmd: cmd, Env: []string{"REDISCLI_AUTH=" + pw}})
		cancel()
		if err != nil {
			return err
		}
		if res.ExitCode != 0 || !strings.Contains(string(res.Stdout), "OK") {
			return fmt.Errorf("redis %s failed: %s", strings.Join(cmd[2:4], " "), redactLine(string(res.Stdout)+string(res.Stderr)))
		}
	}
	return nil
}
