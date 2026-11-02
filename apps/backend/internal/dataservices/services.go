// Package dataservices manages the shared MySQL and PostgreSQL services:
// container specs, administrator secrets, readiness, and add-only app grants.
// Administrator and app secrets never appear on host argv, in labels, or in
// API output; SQL travels over exec stdin or private files.
package dataservices

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// ErrSecretMissing reports an established relational service whose
// administrator secret is gone. The real password lives in the data volume,
// so a newly generated one could never log in.
var ErrSecretMissing = errors.New("administrator secret missing")

// EnsureSecrets generates administrator secrets once. Existing secrets are
// never rotated. For an established service a missing secret is refused
// rather than regenerated.
func (m *Manager) EnsureSecrets(s domain.DataService, established bool) error {
	dir := m.secretsDir(s.Name)
	if err := platform.EnsureDir(filepath.Dir(dir), 0o700, platform.RootOwner); err != nil {
		return err
	}
	if err := platform.EnsureDir(dir, 0o700, platform.RootOwner); err != nil {
		return err
	}
	pwPath := filepath.Join(dir, "root-password")
	pw, err := os.ReadFile(pwPath)
	if errors.Is(err, fs.ErrNotExist) {
		if established {
			return fmt.Errorf("%w: %s", ErrSecretMissing, pwPath)
		}
		pw = []byte(platform.RandomPassword(32))
		if err := platform.AtomicWrite(pwPath, pw, 0o400, platform.RootOwner); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	password := strings.TrimSpace(string(pw))
	if s.Engine == domain.EngineMySQL {
		cnf := fmt.Sprintf("[client]\nuser=root\npassword=%s\nhost=localhost\n", password)
		if _, err := platform.WriteIfChanged(
			filepath.Join(dir, "client.cnf"),
			[]byte(cnf),
			0o400,
			platform.RootOwner,
		); err != nil {
			return err
		}
	}
	return nil
}

// AppClientFile writes a private MySQL option file that logs in as the
// binding user and returns its path inside the service container. The caller
// must call remove when done. Restores replay dumps through it so that
// objects without a DEFINER become the binding user's, never root's.
func (m *Manager) AppClientFile(service, user, password string) (inContainer string, remove func(), err error) {
	if _, err := ident(user); err != nil {
		return "", nil, err
	}
	if !passwordPattern.MatchString(password) {
		return "", nil, errors.New("binding password has an unexpected format")
	}
	name := "app-" + platform.RandomHex(8) + ".cnf"
	host := filepath.Join(m.secretsDir(service), name)
	cnf := fmt.Sprintf("[client]\nuser=%s\npassword=%s\nhost=127.0.0.1\nprotocol=tcp\n", user, password)
	if err := platform.AtomicWrite(host, []byte(cnf), 0o400, platform.RootOwner); err != nil {
		return "", nil, err
	}
	return "/run/bento-secrets/" + name, func() { _ = os.Remove(host) }, nil
}

// AdminPassword reads a service administrator secret (backend-internal only).
func (m *Manager) AdminPassword(service string) (string, error) {
	b, err := os.ReadFile(filepath.Join(m.secretsDir(service), "root-password"))
	return strings.TrimSpace(string(b)), err
}

func (m *Manager) volumeTarget(s domain.DataService) string {
	if s.Engine == domain.EngineMySQL {
		return "/var/lib/mysql"
	}
	// PostgreSQL 18+ images keep PGDATA in a major-versioned subdirectory
	// and refuse a volume mounted at the legacy /var/lib/postgresql/data.
	if major, err := strconv.Atoi(s.Version); err == nil && major >= 18 {
		return "/var/lib/postgresql"
	}
	return "/var/lib/postgresql/data"
}

// ContainerSpec plans the service container. Data services join only the
// private data network and publish no host ports.
func (m *Manager) ContainerSpec(s domain.DataService) docker.ContainerSpec {
	labels := m.Names.Labels(runtime.RoleDatabase, map[string]string{runtime.LabelService: s.Name})
	if fp := SettingsFingerprint(s); fp != "" {
		labels[runtime.LabelServiceSettings] = fp
	}
	mounts := []mount.Mount{
		{Type: mount.TypeVolume, Source: s.Volume, Target: m.volumeTarget(s)},
		{Type: mount.TypeBind, Source: m.secretsDir(s.Name), Target: "/run/bento-secrets", ReadOnly: true},
	}
	cfg := &container.Config{Image: s.Image, Labels: labels, Hostname: s.Name}
	switch s.Engine {
	case domain.EngineMySQL:
		cfg.Env = []string{"MYSQL_ROOT_PASSWORD_FILE=/run/bento-secrets/root-password"}
		cfg.Cmd = ServerArgs(s)
		cfg.Healthcheck = &container.HealthConfig{
			Test:     []string{"CMD", "mysqladmin", "--defaults-extra-file=/run/bento-secrets/client.cnf", "ping", "--silent"},
			Interval: 15 * time.Second, Timeout: 5 * time.Second, StartPeriod: 120 * time.Second, Retries: 5,
		}
	case domain.EnginePostgres:
		cfg.Env = []string{"POSTGRES_PASSWORD_FILE=/run/bento-secrets/root-password"}
		cfg.Cmd = ServerArgs(s)
		cfg.Healthcheck = &container.HealthConfig{
			Test:     []string{"CMD", "pg_isready", "-U", "postgres"},
			Interval: 15 * time.Second, Timeout: 5 * time.Second, StartPeriod: 60 * time.Second, Retries: 5,
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
	return docker.ContainerSpec{
		Name: m.Names.ServiceContainer(s.Name), Config: cfg, HostConfig: host,
		Networking: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			m.Names.DataNetwork(): {Aliases: []string{s.Name}},
		}},
	}
}

// ServerArgs renders tuned settings as the server command, or nil to keep
// the image default. Both official images (and pglayers) run a bare
// "mysqld"/"postgres" CMD and read their remaining configuration from files,
// so appending flags overrides only the tuned parameters. Values come from
// domain.NormalizeServiceSettings and keys from the allowlist; anything else
// is dropped rather than passed through.
func ServerArgs(s domain.DataService) []string {
	keys := slices.Sorted(maps.Keys(s.Settings))
	var args []string
	for _, k := range keys {
		t, ok := domain.Tunable(s.Engine, k)
		if !ok {
			continue
		}
		v := s.Settings[k]
		if t.Kind == domain.TunableSize {
			n, canon, err := domain.ParseSize(v)
			if err != nil || n < t.Min || n > t.Max {
				continue
			}
			v = canon
			if s.Engine == domain.EnginePostgres {
				// PostgreSQL units: kB, MB, GB.
				v = strings.TrimSuffix(canon, "K") + "kB"
				if !strings.HasSuffix(canon, "K") {
					v = canon + "B"
				}
			}
		} else if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			continue
		}
		switch s.Engine {
		case domain.EngineMySQL:
			args = append(args, "--"+strings.ReplaceAll(k, "_", "-")+"="+v)
		case domain.EnginePostgres:
			args = append(args, "-c", k+"="+v)
		}
	}
	if len(args) == 0 {
		return nil
	}
	if s.Engine == domain.EngineMySQL {
		return append([]string{"mysqld"}, args...)
	}
	return append([]string{"postgres"}, args...)
}

// SettingsFingerprint identifies the rendered server settings; it is empty
// when none are tuned, so containers created before tuning existed match.
func SettingsFingerprint(s domain.DataService) string {
	args := ServerArgs(s)
	if len(args) == 0 {
		return ""
	}
	return platform.SHA256Hex([]byte(strings.Join(args, "\x00")))[:16]
}

// Ready runs the engine-specific readiness command inside the container.
func (m *Manager) Ready(ctx context.Context, s domain.DataService, containerID string) error {
	var req docker.ExecRequest
	switch s.Engine {
	// First-boot entrypoints run a temporary socket-only server; requiring a
	// TCP answer ensures the real server is up.
	case domain.EngineMySQL:
		req.Cmd = []string{
			"mysqladmin",
			"--defaults-extra-file=/run/bento-secrets/client.cnf",
			"--protocol=tcp",
			"-h",
			"127.0.0.1",
			"ping",
			"--silent",
		}
	case domain.EnginePostgres:
		req.Cmd = []string{"pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-q"}
	default:
		return fmt.Errorf("unsupported engine %q", s.Engine)
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
		return "", errors.New("not a relational service")
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

// Validation patterns are compiled once: ProvisionBinding checks each
// identifier and generated password.
var (
	identPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	passwordPattern = regexp.MustCompile(`^[A-Za-z0-9]+$`)
)

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
func (m *Manager) ProvisionBinding(
	ctx context.Context,
	s domain.DataService,
	containerID string,
	b domain.Binding,
) error {
	user, err := ident(b.Username)
	if err != nil {
		return err
	}
	if !passwordPattern.MatchString(b.Password) {
		return errors.New("generated password has an unexpected format")
	}
	switch s.Engine {
	case domain.EngineMySQL:
		if _, err := m.SQL(
			ctx,
			s,
			containerID,
			"",
			fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s';\n", user, b.Password),
		); err != nil {
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
	exists, err := m.SQL(
		ctx,
		s,
		id,
		"",
		fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '%s';\n", db),
	)
	if err != nil {
		return err
	}
	if exists != "0" {
		grants, err := m.SQL(
			ctx,
			s,
			id,
			"",
			fmt.Sprintf("SELECT COUNT(*) FROM mysql.db WHERE User = '%s' AND Db = '%s';\n", user, db),
		)
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
	owner, err := m.SQL(
		ctx,
		s,
		id,
		"",
		fmt.Sprintf("SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = '%s';\n", db),
	)
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

// UserExists reports whether a relational user (role) exists on the service.
func (m *Manager) UserExists(ctx context.Context, s domain.DataService, containerID, user string) (bool, error) {
	u, err := ident(user)
	if err != nil {
		return false, err
	}
	q := fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User = '%s';\n", u)
	if s.Engine == domain.EnginePostgres {
		q = fmt.Sprintf("SELECT COUNT(*) FROM pg_roles WHERE rolname = '%s';\n", u)
	}
	out, err := m.SQL(ctx, s, containerID, "", q)
	return out != "0" && out != "", err
}

// DatabaseExists reports whether a relational database exists on the service.
func (m *Manager) DatabaseExists(ctx context.Context, s domain.DataService, containerID, name string) (bool, error) {
	db, err := ident(name)
	if err != nil {
		return false, err
	}
	q := fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '%s';\n", db)
	if s.Engine == domain.EnginePostgres {
		q = fmt.Sprintf("SELECT COUNT(*) FROM pg_database WHERE datname = '%s';\n", db)
	}
	out, err := m.SQL(ctx, s, containerID, "", q)
	return out != "0" && out != "", err
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
