package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/coder/websocket"
	"golang.org/x/term"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/docker"
	"github.com/khanhicetea/bento/apps/backend/internal/domain"
	"github.com/khanhicetea/bento/apps/backend/internal/operations"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/scheduler"
	"github.com/khanhicetea/bento/apps/backend/internal/stack"
)

const usage = `bento — single-host control plane for per-app containers

Usage: bento [--stack ROOT] <command> [arguments]

The stack root comes from --stack or BENTO_STACK_ROOT (no global default).

Backend:
  serve [--listen 127.0.0.1:7780] [--origin URL]... [--utils-listen ADDR|apps:PORT|off]... [--op-concurrency 4]
                                                      run the resident backend (utils routes: /_bento/webhook/*, /_bento/dbadmin/*)
  init --name NAME [--mysql 8.4] [--postgres 17] [--uid-first N --uid-last N] [--password-stdin]
  import --from DIR [--name NEWNAME] [--uid-first N --uid-last N]
                                                      stage an export into an empty root
  version

Client (requires the running backend):
  status
  auth set-password                                   reads the password from the terminal or stdin
  apps
  app show SLUG | create --json FILE | update SLUG --json FILE
  app start|stop|restart|publish|unpublish SLUG
  app remove SLUG                                     prompts for "delete SLUG"
  app bind SLUG --engine mysql|postgres|sqlite [--service NAME]
  app add-db SLUG --binding ID --name NAME
  app logs SLUG [--tail N] [--follow]
  app exec SLUG [--running] [--workdir DIR] -- ARGV...
  app shell SLUG [--running]
  app minicrond SLUG -- ARGS...
  app permissions SLUG --mode check|dry-run|shallow|recursive
  app git SLUG [--repo URL --branch B [--rotate-key] | --remove]   shows the deploy key to add to the repo
  app deploy SLUG                                     clones or resets to the branch, runs ~/deploy.sh, reloads
  app webhook SLUG [--enable | --rotate | --disable]  push-to-deploy URL, secret (shown once) and recent deliveries
  ops [--target ID] | op ID | op cancel ID
  services | service add --engine mysql|postgres --version V
  edge | edge set --json FILE
  tunnel | tunnel set-token (stdin) | tunnel disable
  proxies | proxy set --json FILE | proxy remove NAME
  retired | retired prune APP_ID                      interactive; lists what will be deleted
  backup run [--app SLUG] [--compression zstd|gzip|none] [--upload]
  backup list | backup runs | backup restore --artifact PATH --app SLUG --database DB
  backup schedule list|show ID|create --json FILE|update ID --json FILE|enable ID|disable ID|delete ID
  export --to DIR

Global flags: --json (machine output), --no-wait (return after acceptance)
`

// Main runs the CLI and returns the process exit code.
func Main(args []string, version string) int {
	if len(args) > 0 && args[0] == "internal-relay" {
		if err := scheduler.RunChild(); err != nil {
			fmt.Fprintln(os.Stderr, "relay:", err)
			return 1
		}
		return 0
	}
	g := flag.NewFlagSet("bento", flag.ContinueOnError)
	root := g.String("stack", os.Getenv("BENTO_STACK_ROOT"), "stack root")
	jsonOut := g.Bool("json", false, "machine-readable output")
	noWait := g.Bool("no-wait", false, "do not wait for operations")
	g.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := g.Parse(args); err != nil {
		return 2
	}
	rest := g.Args()
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if rest[0] == "version" {
		fmt.Println("bento", version)
		return 0
	}
	if rest[0] == "help" || rest[0] == "--help" {
		fmt.Print(usage)
		return 0
	}
	layout, err := platform.NewLayout(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	r := &runner{layout: layout, json: *jsonOut, wait: !*noWait, version: version, out: os.Stdout}
	if err := r.run(rest); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if ae, ok := errors.AsType[*APIError](err); ok && ae.Status < 500 {
			return 3
		}
		return 1
	}
	return 0
}

type runner struct {
	layout  platform.Layout
	json    bool
	wait    bool
	version string
	out     io.Writer
}

func (r *runner) client() *Client { return NewClient(r.layout) }

func sub(name string, args []string) (*flag.FlagSet, []string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	return fs, args
}

func readJSONFile(path string, out any) error {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

// readSecret reads one line from the terminal without echo, or from stdin.
func readSecret(prompt string) (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(io.LimitReader(os.Stdin, 8192)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func confirmPrompt(expected string) (string, error) {
	fmt.Fprintf(os.Stderr, "Type %q to confirm: ", expected)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (r *runner) run(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c := r.client()
	switch args[0] {
	case "serve":
		fs, rest := sub("serve", args[1:])
		listen := fs.String("listen", "127.0.0.1:7780", "loopback listen address")
		var origins, utils multiFlag
		fs.Var(&origins, "origin", "additional exact browser origin (repeatable)")
		fs.Var(
			&utils,
			"utils-listen",
			"utils routes listener: IP:PORT, apps:PORT (apps network gateway), or off (repeatable; default 127.0.0.1:7781 and apps:7781)",
		)
		concurrency := fs.Int(
			"op-concurrency",
			operations.DefaultConcurrency,
			fmt.Sprintf("operations executed at once, 1-%d (1 = strictly serial)", MaxOpConcurrency),
		)
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return Serve(ServeOptions{
			Root:          r.layout.Root,
			Listen:        *listen,
			Origins:       origins,
			UtilsListen:   utils,
			OpConcurrency: *concurrency,
			Version:       r.version,
		})
	case "init":
		fs, rest := sub("init", args[1:])
		name := fs.String("name", "", "stack name")
		mysql := fs.String("mysql", "", "initial MySQL version (e.g. 8.4)")
		pg := fs.String("postgres", "", "initial PostgreSQL version (e.g. 17)")
		first := fs.Int("uid-first", 10000, "first app uid")
		last := fs.Int("uid-last", 19999, "last app uid")
		pwStdin := fs.Bool("password-stdin", false, "read the operator password from stdin")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		opts := stack.InitOptions{
			Root:     r.layout.Root,
			Name:     *name,
			UIDRange: domain.UIDRange{First: *first, Last: *last},
			MySQL:    *mysql,
			Postgres: *pg,
		}
		if *pwStdin {
			pw, err := readSecret("")
			if err != nil {
				return err
			}
			opts.Password = pw
		}
		id, err := stack.Init(ctx, opts)
		if err != nil {
			return err
		}
		fmt.Fprintf(
			r.out,
			"initialized stack %s (%s) at %s\nnext: bento --stack %s serve\n",
			id.Name,
			id.ID,
			r.layout.Root,
			r.layout.Root,
		)
		if opts.Password == "" {
			fmt.Fprintf(r.out, "then set the web password: bento --stack %s auth set-password\n", r.layout.Root)
		}
		return nil
	case "import":
		fs, rest := sub("import", args[1:])
		from := fs.String("from", "", "export directory")
		name := fs.String("name", "", "new stack name (required when cloning on the same host)")
		first := fs.Int("uid-first", 0, "move future uid allocations to this range (same-host clones)")
		last := fs.Int("uid-last", 0, "end of the new uid range")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		var rng *domain.UIDRange
		if *first != 0 || *last != 0 {
			rng = &domain.UIDRange{First: *first, Last: *last}
		}
		engine, err := docker.NewSDK()
		if err != nil {
			return err
		}
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		if err := stack.Import(
			ctx,
			engine,
			log,
			stack.ImportOptions{Root: r.layout.Root, From: *from, Name: *name, NewUIDRange: rng},
		); err != nil {
			return err
		}
		fmt.Fprintln(
			r.out,
			"imported. All apps are stopped and unpublished; edge, tunnel, and backup schedule are disabled until you enable them.",
		)
		return nil
	case "status":
		var st dto.SystemStatus
		if err := c.Do(ctx, "GET", "/api/v1/system", nil, &st, nil); err != nil {
			return err
		}
		if r.json {
			printJSON(st)
			return nil
		}
		fmt.Fprintf(
			r.out,
			"stack %s (%s) root %s\nbackend %s since %s\ndocker %s api %s %s %s\napps %d (%d desired running), active operations %d\n",
			st.StackName,
			st.StackID,
			st.Root,
			st.Version,
			st.StartedAt,
			st.DockerVersion,
			st.DockerAPI,
			st.Arch,
			st.DockerError,
			st.Apps,
			st.RunningApps,
			st.QueuedOps,
		)
		return nil
	case "auth":
		if len(args) < 2 || args[1] != "set-password" {
			return errors.New("usage: bento auth set-password")
		}
		pw, err := readSecret("New operator password: ")
		if err != nil {
			return err
		}
		if err := c.Do(ctx, "PUT", "/api/v1/auth/password", dto.SetPasswordRequest{Password: pw}, nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(r.out, "password updated; existing browser sessions were revoked")
		return nil
	case "apps":
		var list dto.AppList
		if err := c.Do(ctx, "GET", "/api/v1/apps", nil, &list, nil); err != nil {
			return err
		}
		if r.json {
			printJSON(list)
			return nil
		}
		tw := tabwriter.NewWriter(r.out, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SLUG\tID\tUID\tRUNTIME\tDESIRED\tOBSERVED\tINGRESS\tPUBLICATION\tDOMAIN")
		for _, a := range list.Apps {
			fmt.Fprintf(
				tw,
				"%s\t%s\t%d\t%s %s\t%s\t%s\t%s\t%s\t%s\n",
				a.Slug,
				a.ID,
				a.UID,
				a.Toolchain,
				a.Version,
				a.DesiredRuntime,
				a.Observed.State,
				a.Ingress,
				a.Publication,
				a.PrimaryDomain,
			)
		}
		return tw.Flush()
	case "app":
		return r.app(ctx, c, args[1:])
	case "ops":
		fs, rest := sub("ops", args[1:])
		target := fs.String("target", "", "filter by target id")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		path := "/api/v1/operations?limit=50"
		if *target != "" {
			path += "&target=" + *target
		}
		var list dto.OperationList
		if err := c.Do(ctx, "GET", path, nil, &list, nil); err != nil {
			return err
		}
		if r.json {
			printJSON(list)
			return nil
		}
		tw := tabwriter.NewWriter(r.out, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSTATE\tPHASE\tCREATED\tERROR")
		for _, o := range list.Operations {
			phase := o.Phase
			if o.WaitingOn != "" {
				phase = "waiting on " + o.WaitingOn
			}
			fmt.Fprintf(
				tw,
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				o.ID,
				o.Kind,
				o.TargetID,
				o.State,
				phase,
				o.CreatedAt,
				o.ErrorMessage,
			)
		}
		return tw.Flush()
	case "op":
		if len(args) == 3 && args[1] == "cancel" {
			var op dto.Operation
			if err := c.Do(ctx, "POST", "/api/v1/operations/"+args[2]+"/cancel", map[string]any{}, &op, nil); err != nil {
				return err
			}
			fmt.Fprintf(r.out, "%s: %s (cancellation is honored at the next safe boundary)\n", op.ID, op.State)
			return nil
		}
		if len(args) != 2 {
			return errors.New("usage: bento op ID | bento op cancel ID")
		}
		var op dto.Operation
		if err := c.Do(ctx, "GET", "/api/v1/operations/"+args[1], nil, &op, nil); err != nil {
			return err
		}
		printJSON(op)
		return nil
	case "services":
		var list dto.ServiceList
		if err := c.Do(ctx, "GET", "/api/v1/services", nil, &list, nil); err != nil {
			return err
		}
		if r.json {
			printJSON(list)
			return nil
		}
		tw := tabwriter.NewWriter(r.out, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tENGINE\tVERSION\tSTATE\tVOLUME")
		for _, s := range list.Services {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Name, s.Engine, s.Version, s.State, s.Volume)
		}
		return tw.Flush()
	case "service":
		fs, rest := sub("service add", args[min(2, len(args)):])
		engine := fs.String("engine", "", "mysql or postgres")
		ver := fs.String("version", "", "version")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if len(args) < 2 || args[1] != "add" {
			return errors.New("usage: bento service add --engine E --version V")
		}
		_, err := c.Mutate(
			ctx,
			"POST",
			"/api/v1/services",
			dto.CreateServiceRequest{Engine: dto.Engine(*engine), Version: *ver},
			r.wait,
			r.out,
		)
		return err
	case "edge":
		if len(args) >= 2 && args[1] == "set" {
			fs, rest := sub("edge set", args[2:])
			file := fs.String("json", "", "settings JSON file ('-' for stdin)")
			if err := fs.Parse(rest); err != nil {
				return err
			}
			var s dto.EdgeSettings
			if err := readJSONFile(*file, &s); err != nil {
				return err
			}
			_, err := c.Mutate(ctx, "PUT", "/api/v1/edge", s, r.wait, r.out)
			return err
		}
		var st dto.EdgeStatus
		if err := c.Do(ctx, "GET", "/api/v1/edge", nil, &st, nil); err != nil {
			return err
		}
		printJSON(st)
		return nil
	case "tunnel":
		if len(args) >= 2 && (args[1] == "set-token" || args[1] == "disable") {
			var token string
			if args[1] == "set-token" {
				var err error
				if token, err = readSecret("Cloudflare tunnel token: "); err != nil {
					return err
				}
				if token == "" {
					return errors.New("empty token (use `bento tunnel disable` to disable)")
				}
			}
			_, err := c.Mutate(ctx, "PUT", "/api/v1/tunnel/token", dto.SetTunnelTokenRequest{Token: token}, r.wait, r.out)
			return err
		}
		var st dto.TunnelStatus
		if err := c.Do(ctx, "GET", "/api/v1/tunnel", nil, &st, nil); err != nil {
			return err
		}
		printJSON(st)
		return nil
	case "proxies":
		var list dto.ProxyList
		if err := c.Do(ctx, "GET", "/api/v1/proxies", nil, &list, nil); err != nil {
			return err
		}
		printJSON(list)
		return nil
	case "proxy":
		if len(args) >= 3 && args[1] == "remove" {
			got, err := confirmPrompt("delete " + args[2])
			if err != nil {
				return err
			}
			_, err = c.Mutate(ctx, "DELETE", "/api/v1/proxies/"+args[2], dto.ConfirmRequest{Confirm: got}, r.wait, r.out)
			return err
		}
		if len(args) >= 2 && args[1] == "set" {
			fs, rest := sub("proxy set", args[2:])
			file := fs.String("json", "", "proxy JSON file")
			if err := fs.Parse(rest); err != nil {
				return err
			}
			var p dto.ProxyRequest
			if err := readJSONFile(*file, &p); err != nil {
				return err
			}
			_, err := c.Mutate(ctx, "POST", "/api/v1/proxies", p, r.wait, r.out)
			return err
		}
		return errors.New("usage: bento proxy set --json FILE | bento proxy remove NAME")
	case "retired":
		return r.retired(ctx, c, args[1:])
	case "backup":
		return r.backup(ctx, c, args[1:])
	case "export":
		fs, rest := sub("export", args[1:])
		to := fs.String("to", "", "empty destination directory outside the stack root")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Export briefly stops running apps and data services to take consistent snapshots.")
		got, err := confirmPrompt("export")
		if err != nil {
			return err
		}
		_, err = c.Mutate(
			ctx,
			"POST",
			"/api/v1/stack/export",
			dto.ExportRequest{Destination: *to, Confirm: got},
			r.wait,
			r.out,
		)
		return err
	}
	return fmt.Errorf("unknown command %q (see bento help)", args[0])
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func (r *runner) app(ctx context.Context, c *Client, args []string) error {
	if len(args) < 2 && !(len(args) == 1 && args[0] == "create") {
		return errors.New("usage: bento app <command> SLUG (see bento help)")
	}
	cmd := args[0]
	switch cmd {
	case "create":
		fs, rest := sub("app create", args[1:])
		file := fs.String("json", "", "CreateAppRequest JSON file ('-' for stdin)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		var req dto.CreateAppRequest
		if err := readJSONFile(*file, &req); err != nil {
			return err
		}
		acc, err := c.Mutate(ctx, "POST", "/api/v1/apps", req, r.wait, r.out)
		if err == nil && acc.App != nil {
			fmt.Fprintf(
				r.out,
				"app %s id %s uid %d (stopped and unpublished; start it with `bento app start %s`)\n",
				acc.App.Slug,
				acc.App.ID,
				acc.App.UID,
				acc.App.Slug,
			)
		}
		return err
	}
	slug := args[1]
	base := "/api/v1/apps/" + slug
	switch cmd {
	case "show":
		var a dto.App
		if err := c.Do(ctx, "GET", base, nil, &a, nil); err != nil {
			return err
		}
		printJSON(a)
		return nil
	case "update":
		fs, rest := sub("app update", args[2:])
		file := fs.String("json", "", "UpdateAppRequest JSON file")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		var req dto.UpdateAppRequest
		if err := readJSONFile(*file, &req); err != nil {
			return err
		}
		_, err := c.Mutate(ctx, "PATCH", base, req, r.wait, r.out)
		return err
	case "start", "stop", "restart", "publish", "unpublish":
		if cmd == "restart" {
			fmt.Fprintln(os.Stderr, "note: restarting the container also interrupts this app's scheduler and workers")
		}
		_, err := c.Mutate(ctx, "POST", base+"/"+cmd, map[string]any{}, r.wait, r.out)
		return err
	case "remove":
		fmt.Fprintln(
			os.Stderr,
			"Removing stops and deletes the app's containers and retires its identity. Home, SQLite files, and databases are retained.",
		)
		got, err := confirmPrompt("delete " + slug)
		if err != nil {
			return err
		}
		_, err = c.Mutate(ctx, "DELETE", base, dto.ConfirmRequest{Confirm: got}, r.wait, r.out)
		return err
	case "bind":
		fs, rest := sub("app bind", args[2:])
		engine := fs.String("engine", "", "mysql, postgres, or sqlite")
		svc := fs.String("service", "", "data service name (relational only)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		_, err := c.Mutate(
			ctx,
			"POST",
			base+"/bindings",
			dto.BindingRequest{Engine: dto.Engine(*engine), Service: *svc},
			r.wait,
			r.out,
		)
		return err
	case "add-db":
		fs, rest := sub("app add-db", args[2:])
		binding := fs.String("binding", "", "binding id")
		name := fs.String("name", "", "database suffix")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		_, err := c.Mutate(
			ctx,
			"POST",
			base+"/bindings/"+*binding+"/databases",
			dto.AddDatabaseRequest{Name: *name},
			r.wait,
			r.out,
		)
		return err
	case "permissions":
		fs, rest := sub("app permissions", args[2:])
		mode := fs.String("mode", "check", "check, dry-run, shallow, recursive")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		acc, err := c.Mutate(ctx, "POST", base+"/permissions", dto.PermissionsRequest{Mode: *mode}, r.wait, r.out)
		if err == nil {
			printJSON(acc.Operation.Result)
		}
		return err
	case "git":
		fs, rest := sub("app git", args[2:])
		repo := fs.String("repo", "", "git@host:owner/repo.git, ssh://..., or https://... (public only)")
		branch := fs.String("branch", "main", "branch to deploy")
		rotate := fs.Bool("rotate-key", false, "replace the deploy key")
		remove := fs.Bool("remove", false, "forget the git source and destroy its deploy key")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		var g dto.GitSource
		var err error
		switch {
		case *remove:
			err = c.Do(ctx, "DELETE", base+"/git", map[string]any{}, &g, nil)
		case *repo != "":
			err = c.Do(
				ctx,
				"PUT",
				base+"/git",
				dto.GitSourceRequest{RepoURL: *repo, Branch: *branch, RotateKey: *rotate},
				&g,
				nil,
			)
		default:
			err = c.Do(ctx, "GET", base+"/git", nil, &g, nil)
		}
		if err != nil {
			return err
		}
		if r.json {
			printJSON(g)
			return nil
		}
		if !g.Configured {
			fmt.Fprintf(r.out, "no git source (set one with `bento app git %s --repo URL --branch main`)\n", slug)
			return nil
		}
		fmt.Fprintf(r.out, "repo:     %s\nbranch:   %s\n", g.RepoURL, g.Branch)
		if g.DeployedCommit != "" {
			fmt.Fprintf(r.out, "deployed: %s at %s\n", g.DeployedCommit, g.DeployedAt)
		}
		if g.UsesSSH {
			fmt.Fprintf(
				r.out,
				"\nAdd this read-only deploy key to the repository (GitHub: Settings > Deploy keys):\n\n%s\n\nfingerprint: %s\n",
				g.PublicKey,
				g.Fingerprint,
			)
		}
		return nil
	case "deploy":
		acc, err := c.Mutate(ctx, "POST", base+"/deploy", map[string]any{}, r.wait, r.out)
		if err == nil && r.wait {
			printJSON(acc.Operation.Result)
		}
		return err
	case "webhook":
		fs, rest := sub("app webhook", args[2:])
		enable := fs.Bool("enable", false, "create the webhook (or rotate its secret)")
		rotate := fs.Bool("rotate", false, "replace the secret; the URL is kept")
		disable := fs.Bool("disable", false, "destroy the webhook")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		var hook dto.WebhookSecret
		var err error
		switch {
		case *disable:
			err = c.Do(ctx, "DELETE", base+"/webhook", map[string]any{}, &hook.Webhook, nil)
		case *enable || *rotate:
			err = c.Do(ctx, "POST", base+"/webhook", map[string]any{}, &hook, nil)
		default:
			err = c.Do(ctx, "GET", base+"/webhook", nil, &hook.Webhook, nil)
		}
		if err != nil {
			return err
		}
		if r.json {
			if hook.Secret != "" {
				printJSON(hook)
			} else {
				printJSON(hook.Webhook)
			}
			return nil
		}
		if !hook.Enabled {
			fmt.Fprintf(r.out, "no webhook (enable one with `bento app webhook %s --enable`)\n", slug)
			return nil
		}
		if hook.URL != "" {
			fmt.Fprintf(r.out, "url:    %s\n", hook.URL)
		} else {
			fmt.Fprintf(r.out, "path:   %s  (on any domain whose /_bento/webhook/* reaches Bento)\n", hook.Path)
		}
		if len(hook.Targets) > 0 {
			fmt.Fprintf(
				r.out,
				"expose: /_bento/webhook/* -> %s  (host nginx, Cloudflare Tunnel path rule)\n",
				strings.Join(hook.Targets, " or "),
			)
		}
		if hook.Secret != "" {
			fmt.Fprintf(
				r.out,
				"secret: %s\n\nThis secret is shown only once. Use it as the webhook secret (GitHub, Gitea, Forgejo,\n"+
					"Bitbucket), the secret token (GitLab), or `Authorization: Bearer <secret>` (curl/CI).\n",
				hook.Secret,
			)
		}
		for _, d := range hook.Deliveries {
			fmt.Fprintf(r.out, "%s  %-9s %-10s %-12s %s %s\n", d.At, d.Provider, d.Event, d.Result, d.OperationID, d.Detail)
		}
		return nil
	case "logs":
		fs, rest := sub("app logs", args[2:])
		tail := fs.Int("tail", 200, "lines")
		follow := fs.Bool("follow", false, "follow")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return r.logs(ctx, c, base, *tail, *follow)
	case "exec":
		fs, rest := sub("app exec", args[2:])
		running := fs.Bool("running", false, "exec into the running instance")
		workdir := fs.String("workdir", "", "working directory relative to the app code directory")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		argv := fs.Args()
		if len(argv) == 0 {
			return errors.New("usage: bento app exec SLUG [--running] -- ARGV...")
		}
		var res dto.ExecResult
		if err := c.Do(
			ctx,
			"POST",
			base+"/exec",
			dto.ExecRequest{Argv: argv, Workdir: *workdir, Running: *running},
			&res,
			nil,
		); err != nil {
			return err
		}
		os.Stdout.WriteString(res.Stdout)
		os.Stderr.WriteString(res.Stderr)
		if res.Truncated {
			fmt.Fprintln(os.Stderr, "(output truncated)")
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("command exited %d", res.ExitCode)
		}
		return nil
	case "minicrond":
		argv := args[2:]
		if len(argv) > 0 && argv[0] == "--" {
			argv = argv[1:]
		}
		var res dto.ExecResult
		if err := c.Do(
			ctx,
			"POST",
			base+"/scheduler/command",
			dto.SchedulerCommandRequest{Argv: argv},
			&res,
			nil,
		); err != nil {
			return err
		}
		os.Stdout.WriteString(res.Stdout)
		os.Stderr.WriteString(res.Stderr)
		if res.ExitCode != 0 {
			return fmt.Errorf("minicrond exited %d", res.ExitCode)
		}
		return nil
	case "shell":
		fs, rest := sub("app shell", args[2:])
		running := fs.Bool("running", false, "exec into the running instance")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return r.shell(ctx, c, slug, *running)
	}
	return fmt.Errorf("unknown app command %q", cmd)
}

func (r *runner) logs(ctx context.Context, c *Client, base string, tail int, follow bool) error {
	path := fmt.Sprintf("%s/logs?tail=%d", base, tail)
	if follow {
		path += "&follow=1"
	}
	req, err := httpRequest(ctx, "GET", "http://bento"+path)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("logs unavailable (HTTP %d)", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 512<<10)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]string
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil && ev["line"] != "" {
			fmt.Fprintln(r.out, ev["line"])
		}
	}
	return nil
}

func (r *runner) shell(ctx context.Context, c *Client, slug string, running bool) error {
	cols, rows := 120, 32
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		if w, h, err := term.GetSize(fd); err == nil {
			cols, rows = w, h
		}
	}
	mode := "tool"
	if running {
		mode = "running"
	}
	url := fmt.Sprintf("ws://bento/api/v1/apps/%s/terminal?mode=%s&cols=%d&rows=%d", slug, mode, cols, rows)
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: c.HTTP})
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	if term.IsTerminal(fd) {
		old, err := term.MakeRaw(fd)
		if err == nil {
			defer term.Restore(fd, old)
		}
		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
		// signal.Stop never closes winch, so the loop also ends with the shell.
		winchCtx, stopWinch := context.WithCancel(ctx)
		defer stopWinch()
		go func() {
			for {
				select {
				case <-winchCtx.Done():
					return
				case <-winch:
				}
				if w, h, err := term.GetSize(fd); err == nil {
					msg, _ := json.Marshal(map[string]any{"type": "resize", "cols": w, "rows": h})
					_ = conn.Write(winchCtx, websocket.MessageText, msg)
				}
			}
		}()
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	var exit int
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageBinary {
			os.Stdout.Write(data)
			continue
		}
		var ctl struct {
			Type string `json:"type"`
			Code int    `json:"code"`
		}
		if json.Unmarshal(data, &ctl) == nil && ctl.Type == "exit" {
			exit = ctl.Code
		}
	}
	if exit != 0 {
		return fmt.Errorf("shell exited %d", exit)
	}
	return nil
}

func (r *runner) retired(ctx context.Context, c *Client, args []string) error {
	var list dto.RetiredList
	if err := c.Do(ctx, "GET", "/api/v1/retired", nil, &list, nil); err != nil {
		return err
	}
	if len(args) == 0 {
		printJSON(list)
		return nil
	}
	if len(args) != 2 || args[0] != "prune" {
		return errors.New("usage: bento retired | bento retired prune APP_ID")
	}
	for _, ra := range list.Retired {
		if ra.AppID != args[1] {
			continue
		}
		if ra.PrunedAt != "" {
			return fmt.Errorf("already pruned at %s", ra.PrunedAt)
		}
		fmt.Fprintf(
			os.Stderr,
			"Permanently delete retained data of %s (%s, uid %d, uid is NOT reclaimed):\n",
			ra.Slug,
			ra.AppID,
			ra.UID,
		)
		fmt.Fprintf(os.Stderr, "  home: %s\n", ra.Home)
		for _, id := range ra.SQLiteFileIDs {
			fmt.Fprintf(os.Stderr, "  sqlite directory: %s\n", id)
		}
		for _, rel := range ra.Relational {
			fmt.Fprintf(os.Stderr, "  %s on %s: user %s, databases %v\n", rel.Engine, rel.Service, rel.Username, rel.Databases)
		}
		got, err := confirmPrompt("delete")
		if err != nil {
			return err
		}
		_, err = c.Mutate(ctx, "POST", "/api/v1/retired/"+ra.AppID+"/prune", dto.ConfirmRequest{Confirm: got}, r.wait, r.out)
		return err
	}
	return fmt.Errorf("no retired app %s", args[1])
}

func (r *runner) backup(ctx context.Context, c *Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bento backup run|list|runs|restore|schedule")
	}
	switch args[0] {
	case "run":
		fs, rest := sub("backup run", args[1:])
		app := fs.String("app", "", "only this app")
		db := fs.String("database", "", "only these comma-separated databases of --app")
		comp := fs.String("compression", "zstd", "zstd or gzip")
		remote := fs.String("upload", "", "upload new artifacts to this rclone name:path")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		req := dto.BackupRequest{Scope: "all", Compression: *comp, RcloneRemote: *remote}
		if *app != "" {
			req.Scope, req.AppID = "app", *app
		}
		if *db != "" {
			req.Scope, req.Databases = "database", strings.Split(*db, ",")
		}
		acc, err := c.Mutate(ctx, "POST", "/api/v1/backups", req, r.wait, r.out)
		if err == nil && r.wait {
			printJSON(acc.Operation.Result)
		}
		return err
	case "list":
		var list dto.BackupArtifactList
		if err := c.Do(ctx, "GET", "/api/v1/backups/artifacts", nil, &list, nil); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(r.out, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tENGINE\tDATABASE\tBYTES\tCREATED")
		for _, a := range list.Artifacts {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", a.Path, a.Engine, a.Database, a.SizeBytes, a.CreatedAt)
		}
		return tw.Flush()
	case "runs":
		var list dto.BackupRunList
		if err := c.Do(ctx, "GET", "/api/v1/backups/runs", nil, &list, nil); err != nil {
			return err
		}
		printJSON(list)
		return nil
	case "restore":
		fs, rest := sub("backup restore", args[1:])
		art := fs.String("artifact", "", "artifact path from `backup list`")
		app := fs.String("app", "", "app slug")
		db := fs.String("database", "", "database name (or SQLite file id)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Restore REPLACES the contents of %s with %s.\n", *db, *art)
		got, err := confirmPrompt("replace " + *db)
		if err != nil {
			return err
		}
		_, err = c.Mutate(
			ctx,
			"POST",
			"/api/v1/backups/restore",
			dto.RestoreRequest{Artifact: *art, AppID: *app, Database: *db, Confirm: got},
			r.wait,
			r.out,
		)
		return err
	case "schedule", "schedules":
		return r.backupSchedule(ctx, c, args[1:])
	}
	return fmt.Errorf("unknown backup command %q", args[0])
}

func (r *runner) backupSchedule(ctx context.Context, c *Client, args []string) error {
	const base = "/api/v1/backups/schedules"
	usage := errors.New("usage: bento backup schedule list|show ID|create --json FILE|update ID --json FILE|enable ID|disable ID|delete ID")
	if len(args) == 0 {
		args = []string{"list"}
	}
	var out dto.BackupSchedule
	switch args[0] {
	case "list":
		var list dto.BackupScheduleList
		if err := c.Do(ctx, "GET", base, nil, &list, nil); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(r.out, 2, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "ID\tNAME\tENABLED\tCRON (%s)\tSCOPE\tKEEP\tNEXT\tLAST\tSTATE\n", list.TimeZone)
		for _, s := range list.Schedules {
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\t%d\t%s\t%s\t%s\n", s.ID, s.Name, s.Enabled, s.Cron, s.Scope, s.Retain,
				s.NextRun, s.LastRun, s.LastState)
		}
		return tw.Flush()
	case "show", "delete", "enable", "disable":
		if len(args) != 2 {
			return usage
		}
		path := base + "/" + url.PathEscape(args[1])
		switch args[0] {
		case "show":
			if err := c.Do(ctx, "GET", path, nil, &out, nil); err != nil {
				return err
			}
		case "delete":
			return c.Do(ctx, "DELETE", path, nil, nil, nil)
		default:
			req := dto.BackupScheduleEnable{Enabled: args[0] == "enable"}
			if err := c.Do(ctx, "POST", path+"/enabled", req, &out, nil); err != nil {
				return err
			}
		}
	case "create", "update":
		name := "backup schedule " + args[0]
		rest := args[1:]
		path := base
		if args[0] == "update" {
			if len(rest) == 0 {
				return usage
			}
			path += "/" + url.PathEscape(rest[0])
			rest = rest[1:]
		}
		fs, rest := sub(name, rest)
		file := fs.String("json", "", "schedule JSON file")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *file == "" {
			return usage
		}
		var in dto.BackupSchedule
		if err := readJSONFile(*file, &in); err != nil {
			return err
		}
		method := "POST"
		if args[0] == "update" {
			method = "PUT"
		}
		if err := c.Do(ctx, method, path, in, &out, nil); err != nil {
			return err
		}
	default:
		return usage
	}
	printJSON(out)
	return nil
}
