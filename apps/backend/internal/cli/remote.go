package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
)

// serviceRemote is `bento service remote NAME [on|off|rotate]`: without an
// action it shows the state, address and forward command.
func (r *runner) serviceRemote(ctx context.Context, c *Client, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: bento service remote NAME [on|off|rotate]")
	}
	name := args[0]
	if len(args) == 2 {
		action := dto.RemoteAccessAction(args[1])
		switch action {
		case dto.RemoteAccessActionOn, dto.RemoteAccessActionOff, dto.RemoteAccessActionRotate:
		default:
			return errors.New("usage: bento service remote NAME [on|off|rotate]")
		}
		if action == dto.RemoteAccessActionRotate && !r.json {
			fmt.Fprintln(r.out, "Rotating: the current address stops working and open tunnels are cut.")
		}
		if _, err := c.Mutate(ctx, "PUT", "/api/v1/services/"+url.PathEscape(name)+"/remote",
			dto.RemoteAccessRequest{Action: action}, r.wait, r.out); err != nil {
			return err
		}
		if !r.wait || action == dto.RemoteAccessActionOff {
			return nil
		}
	}
	var list dto.ServiceList
	if err := c.Do(ctx, "GET", "/api/v1/services", nil, &list, nil); err != nil {
		return err
	}
	for _, s := range list.Services {
		if s.Name != name {
			continue
		}
		ra := s.RemoteAccess
		if r.json {
			r.printJSON(ra)
			return nil
		}
		tw := tabwriter.NewWriter(r.out, 2, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "REMOTE ACCESS\t%s (experimental)\n", ra.State)
		if ra.Enabled {
			fmt.Fprintf(tw, "CONNECTIONS\t%d / %d\n", ra.Connections, ra.MaxConnections)
		}
		if ra.Message != "" {
			fmt.Fprintf(tw, "MESSAGE\t%s\n", ra.Message)
		}
		if ra.Address != "" {
			fmt.Fprintf(tw, "ADDRESS\t%s\n", ra.Address)
			fmt.Fprintf(tw, "COMMAND\t%s\n", ra.Command)
			fmt.Fprintf(tw, "CONNECT\t127.0.0.1:%d with a binding login (bento app login SLUG)\n", ra.LocalPort)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if ra.Address != "" {
			fmt.Fprintln(r.out, "The address and a binding password together grant access to the database. Share neither.")
		}
		return nil
	}
	return fmt.Errorf("no data service %q", name)
}

// appLogin is `bento app login SLUG [--binding ID]`.
func (r *runner) appLogin(ctx context.Context, c *Client, base, binding string) error {
	if binding == "" {
		var a dto.App
		if err := c.Do(ctx, "GET", base, nil, &a, nil); err != nil {
			return err
		}
		var ids []string
		for _, b := range a.Bindings {
			if b.Engine == dto.EngineMySQL || b.Engine == dto.EnginePostgres {
				ids = append(ids, b.ID)
			}
		}
		switch len(ids) {
		case 0:
			return errors.New("the app has no MySQL or PostgreSQL binding")
		case 1:
			binding = ids[0]
		default:
			return fmt.Errorf("the app has several bindings; choose one with --binding (%s)", strings.Join(ids, ", "))
		}
	}
	var out dto.BindingLogin
	if err := c.Do(ctx, "POST", base+"/bindings/"+url.PathEscape(binding)+"/login", map[string]any{}, &out, nil); err != nil {
		return err
	}
	if r.json {
		r.printJSON(out)
		return nil
	}
	tw := tabwriter.NewWriter(r.out, 2, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "ENGINE\t%s (%s)\n", out.Engine, out.Service)
	fmt.Fprintf(tw, "HOST\t%s\n", out.Host)
	fmt.Fprintf(tw, "PORT\t%d\n", out.Port)
	fmt.Fprintf(tw, "USER\t%s\n", out.Username)
	fmt.Fprintf(tw, "PASSWORD\t%s\n", out.Password)
	fmt.Fprintf(tw, "DATABASES\t%s\n", strings.Join(out.Databases, ", "))
	for _, u := range out.URLs {
		fmt.Fprintf(tw, "URL\t%s\n", u.URL)
	}
	return tw.Flush()
}
