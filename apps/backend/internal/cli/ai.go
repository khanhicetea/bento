package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/khanhicetea/bento/apps/backend/internal/api"
	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/mcp"
)

// runMCP serves MCP on stdio for a local AI client. Every tool call goes
// to the backend over the control socket with the chosen scope, which the
// backend enforces; stdout carries only protocol messages.
func (r *runner) runMCP(ctx context.Context, args []string) error {
	fs, rest := sub("mcp", args)
	sc := fs.String("scope", "read", "access for the AI client: read, operate or admin")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("mcp: unexpected argument %q", fs.Arg(0))
	}
	scope := dto.TokenScope(*sc)
	switch scope {
	case dto.TokenScopeRead, dto.TokenScopeOperate, dto.TokenScopeAdmin:
	default:
		return errors.New("--scope must be read, operate or admin")
	}
	c := r.client()
	hdr := map[string]string{api.ScopeHeader: string(scope)}
	// Fail at startup, not on the first tool call, when the backend is down.
	if err := c.Do(ctx, "GET", "/api/v1/system", nil, nil, hdr); err != nil {
		return err
	}
	b := &mcp.Backend{HTTP: c.HTTP, Header: http.Header{api.ScopeHeader: {string(scope)}}}
	fmt.Fprintf(os.Stderr, "bento mcp: serving %s tools for %s on stdio\n", scope, r.layout.Root)
	return mcp.NewServer(b, scope, r.version).ServeStdio(ctx, os.Stdin, os.Stdout)
}

const tokenUsage = "usage: bento token list | create NAME --scope read|operate|admin [--expires-days N] | revoke NAME|ID"

func (r *runner) runToken(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(tokenUsage)
	}
	c := r.client()
	switch args[0] {
	case "list":
		if err := r.listFlags(flag.NewFlagSet("token list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		var list dto.APITokenList
		if err := c.Do(ctx, "GET", "/api/v1/tokens", nil, &list, nil); err != nil {
			return err
		}
		if r.json {
			r.printJSON(list)
			return nil
		}
		tw := tabwriter.NewWriter(r.out, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSCOPE\tCREATED\tEXPIRES\tLAST USED\tID")
		for _, t := range list.Tokens {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, t.Scope, t.CreatedAt, dash(t.ExpiresAt, "never"),
				dash(t.LastUsedAt, "-"), t.ID)
		}
		return tw.Flush()
	case "create":
		if len(args) < 2 {
			return errors.New(tokenUsage)
		}
		fs, rest := sub("token create", args[2:])
		sc := fs.String("scope", "", "read, operate or admin")
		days := fs.Int("expires-days", 90, "days until the token expires; 0 = never")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *sc == "" {
			return errors.New("--scope is required: read, operate or admin")
		}
		var out dto.CreatedAPIToken
		req := dto.CreateAPITokenRequest{Name: args[1], Scope: dto.TokenScope(*sc), ExpiresInDays: *days}
		if err := c.Do(ctx, "POST", "/api/v1/tokens", req, &out, nil); err != nil {
			return err
		}
		if r.json {
			r.printJSON(out)
			return nil
		}
		fmt.Fprintf(r.out, "created token %s (%s, expires %s)\n\n  %s\n\n", out.Name, out.Scope,
			dash(out.ExpiresAt, "never"), out.Token)
		fmt.Fprintln(r.out, "This is the only time the token is shown. Use it as a bearer token for")
		fmt.Fprintln(r.out, "the MCP endpoint POST <management URL>/mcp, e.g. through an SSH tunnel.")
		return nil
	case "revoke":
		if len(args) != 2 {
			return errors.New(tokenUsage)
		}
		if err := c.Do(ctx, "DELETE", "/api/v1/tokens/"+url.PathEscape(args[1]), nil, nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(r.out, "revoked token %s\n", args[1])
		return nil
	}
	return errors.New(tokenUsage)
}

func dash(v, empty string) string {
	if v == "" {
		return empty
	}
	return v
}
