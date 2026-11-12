package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/khanhicetea/bento/apps/backend/internal/api/dto"
	"github.com/khanhicetea/bento/apps/backend/internal/platform"
	"github.com/khanhicetea/bento/apps/backend/internal/release"
)

// Self-upgrade: `bento upgrade` replaces the binary it runs as with a GitHub
// release whose SHA-256 matches that release's SHA256SUMS, previews every
// running stack with the new binary's `upgrade plan`, and restarts the
// bento@ services that run this binary. It is an operator command on the
// host, like bootstrap.sh, not a backend operation: the backend never
// replaces itself.

// UnitFile is the systemd template unit bootstrap.sh installs.
const UnitFile = "/etc/systemd/system/bento@.service"

// unitSource is the unit's path in the repository at a release tag.
const unitSource = "deploy/systemd/bento@.service"

func releaseSource(version string) release.Source {
	return release.GitHub(os.Getenv("BENTO_REPO"), "bento-upgrade/"+version)
}

func (r *runner) upgradeCheck(ctx context.Context, args []string) error {
	if err := r.listFlags(flag.NewFlagSet("upgrade check", flag.ContinueOnError), args); err != nil {
		return err
	}
	src := releaseSource(r.version)
	tag, err := src.Latest(ctx)
	if err != nil {
		return err
	}
	newer := isNewer(tag, r.version)
	if r.json {
		r.printJSON(map[string]any{"installed": r.version, "latest": tag, "updateAvailable": newer, "repo": src.Repo})
		return nil
	}
	fmt.Fprintf(r.out, "installed %s, latest release %s (%s)\n", r.version, tag, src.Repo)
	if newer {
		fmt.Fprintf(r.out, "update available: sudo bento upgrade (previews every stack before installing)\n")
	} else {
		fmt.Fprintln(r.out, "up to date")
	}
	return nil
}

// isNewer reports whether tag is a later version than installed. An
// unparsable installed version (a local build) counts as older.
func isNewer(tag, installed string) bool {
	t, ok := release.ParseVersion(tag)
	if !ok {
		return false
	}
	cur, ok := release.ParseVersion(installed)
	return !ok || release.Compare(t, cur) > 0
}

// service is one running bento@ unit.
type service struct {
	Unit string
	Root string
	// Exe is the binary the process runs (" (deleted)" removed).
	Exe string
	// UtilsListen are its --utils-listen arguments.
	UtilsListen []string
}

func (r *runner) selfUpgrade(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	tagFlag := fs.String("version", "", "release tag to install (default: the latest release)")
	yes := fs.Bool("yes", false, "install without asking to confirm (the preview still runs and can block)")
	downgrade := fs.Bool("allow-downgrade", false, "install a release older than this binary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("upgrade: unexpected argument %q", fs.Arg(0))
	}
	if os.Geteuid() != 0 {
		return errors.New("upgrade replaces the bento binary and restarts its services: run it as root")
	}
	arch := goruntime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("no Bento release for %s", arch)
	}
	src := releaseSource(r.version)
	tag := *tagFlag
	if tag == "" {
		var err error
		if tag, err = src.Latest(ctx); err != nil {
			return err
		}
	}
	if !release.ValidTag(tag) {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	want, _ := release.ParseVersion(tag)
	if cur, ok := release.ParseVersion(r.version); ok {
		switch c := release.Compare(want, cur); {
		case c == 0:
			fmt.Fprintf(r.out, "already on %s (%s)\n", tag, r.version)
			return nil
		case c < 0 && !*downgrade:
			return fmt.Errorf("%s is older than this binary (%s); pass --allow-downgrade to install it anyway, and "+
				"check that it can still open the state (`upgrade plan` refuses a newer schema)", tag, r.version)
		}
	}
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if target, err = filepath.EvalSymlinks(target); err != nil {
		return err
	}
	step := func(format string, a ...any) { fmt.Fprintf(r.out, "==> "+format+"\n", a...) }

	// Download beside the target so the final rename is atomic.
	tmp, err := os.MkdirTemp(filepath.Dir(target), ".bento-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	step("Downloading %s %s from %s", release.AssetName(arch), tag, src.Repo)
	newBin, err := src.Download(ctx, tag, release.AssetName(arch), tmp)
	if err != nil {
		return err
	}
	fmt.Fprintln(r.out, "    SHA-256 matches the release's SHA256SUMS")
	newVersion, err := binaryVersion(ctx, newBin)
	if err != nil {
		return fmt.Errorf("the downloaded binary does not run: %w", err)
	}
	if got, ok := release.ParseVersion(newVersion); !ok || release.Compare(got, want) != 0 {
		return fmt.Errorf("the %s binary reports version %q; refusing it", tag, newVersion)
	}
	unit, err := src.File(ctx, tag, unitSource, 64<<10)
	if err != nil {
		fmt.Fprintf(r.out, "    ! could not download the systemd unit (%v); the installed one is kept\n", err)
		unit = nil
	}

	services, others, err := runningServices(ctx, target)
	if err != nil {
		return err
	}
	for _, s := range others {
		fmt.Fprintf(r.out, "    ! %s runs %s, not %s: it is not upgraded or restarted\n", s.Unit, s.Exe, target)
	}

	step("Previewing with %s", newVersion)
	if len(services) == 0 {
		fmt.Fprintln(r.out, "    no running bento@ service uses this binary; nothing to preview or restart")
	} else if !hasUpgradePlan(ctx, newBin) {
		fmt.Fprintf(r.out, "    ! %s has no `upgrade plan`; it cannot preview what it changes\n", tag)
	} else {
		for _, s := range services {
			fmt.Fprintf(r.out, "\n--- %s\n", s.Unit)
			planArgs := []string{"--stack", s.Root, "upgrade", "plan"}
			for _, u := range s.UtilsListen {
				planArgs = append(planArgs, "--utils-listen", u)
			}
			cmd := exec.CommandContext(ctx, newBin, planArgs...)
			cmd.Stdout, cmd.Stderr = r.out, r.out
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("%s cannot serve %s (%s); nothing was installed", tag, s.Root, s.Unit)
			}
		}
		fmt.Fprintln(r.out)
	}

	step("Ready to install %s", newVersion)
	fmt.Fprintf(r.out, "    replace %s (%s); the current binary is kept as %s.prev\n", target, r.version, target)
	unitChanged := false
	if unit != nil {
		if cur, err := os.ReadFile(UnitFile); err == nil && !bytes.Equal(cur, unit) {
			unitChanged = true
			fmt.Fprintf(r.out, "    update %s and reload systemd\n", UnitFile)
		}
	}
	for _, s := range services {
		fmt.Fprintf(r.out, "    restart %s (%s): apps, edge and databases keep serving; management pauses\n",
			s.Unit, s.Root)
	}
	if !*yes {
		got, err := confirmPrompt("upgrade")
		if err != nil {
			return err
		}
		if got != "upgrade" {
			return errors.New("confirmation did not match; nothing was installed")
		}
	}

	step("Installing")
	if err := installBinary(target, newBin); err != nil {
		return err
	}
	fmt.Fprintf(r.out, "    %s is %s\n", target, newVersion)
	if unitChanged {
		if err := platform.AtomicWrite(UnitFile, unit, 0o644, platform.RootOwner); err != nil {
			return fmt.Errorf("binary installed, but writing %s failed: %w", UnitFile, err)
		}
		if out, err := exec.CommandContext(ctx, "systemctl", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("binary installed, but systemctl daemon-reload failed: %v: %s", err, out)
		}
		fmt.Fprintf(r.out, "    %s updated\n", UnitFile)
	}

	var failed []string
	for _, s := range services {
		step("Restarting %s", s.Unit)
		if err := restartService(ctx, s, newVersion); err != nil {
			fmt.Fprintf(r.out, "    ! %v\n", err)
			failed = append(failed, s.Unit)
			continue
		}
		fmt.Fprintf(r.out, "    serving with %s\n", newVersion)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s did not come back: see journalctl -u %s. To go back: install -m 0755 %s.prev %s && "+
			"systemctl restart %s (only if the state schema did not change; the first start of a migrating "+
			"release copies the state to state-backups/ first)", strings.Join(failed, ", "), failed[0], target,
			target, strings.Join(failed, " "))
	}
	if len(services) > 0 {
		fmt.Fprintln(r.out, "\nDone. Check Activity: changed images build first, then apps change one at a time.")
	} else {
		fmt.Fprintln(r.out, "\nDone. Restart each bento@<stack> service that should run it.")
	}
	return nil
}

func binaryVersion(ctx context.Context, bin string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) != 2 || f[0] != "bento" {
		return "", fmt.Errorf("unexpected version output %q", out)
	}
	return f[1], nil
}

// hasUpgradePlan reports whether bin knows `upgrade plan` (releases before it
// do not).
func hasUpgradePlan(ctx context.Context, bin string) bool {
	out, _ := exec.CommandContext(ctx, bin, "help").Output()
	return bytes.Contains(out, []byte("upgrade plan"))
}

// installBinary keeps target as target.prev and renames newBin (in the same
// directory tree, so on the same filesystem) over target.
func installBinary(target, newBin string) error {
	cur, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if err := platform.AtomicWrite(target+".prev", cur, 0o755, platform.RootOwner); err != nil {
		return fmt.Errorf("keep the current binary: %w", err)
	}
	if err := os.Chmod(newBin, 0o755); err != nil {
		return err
	}
	if err := os.Rename(newBin, target); err != nil {
		return fmt.Errorf("install %s: %w", target, err)
	}
	return nil
}

// restartService restarts one unit and waits until its backend answers with
// version.
func restartService(ctx context.Context, s service, version string) error {
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", s.Unit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart %s: %v: %s", s.Unit, err, bytes.TrimSpace(out))
	}
	c := NewClient(platform.Layout{Root: s.Root})
	deadline := time.Now().Add(2 * time.Minute)
	var last error
	for time.Now().Before(deadline) {
		var st dto.SystemStatus
		err := c.Do(ctx, "GET", "/api/v1/system", nil, &st, nil)
		if err == nil && st.Version == version {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("backend reports %s", st.Version)
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("%s did not answer as %s within 2 minutes: %v", s.Unit, version, last)
}

// runningServices lists running bento@ units, split into those whose process
// runs target and the others.
func runningServices(ctx context.Context, target string) (mine, others []service, err error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, nil, nil
	}
	out, err := exec.CommandContext(ctx, "systemctl", "list-units", "--type=service", "--state=running",
		"--plain", "--no-legend", "bento@*").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("systemctl list-units: %w", err)
	}
	for _, unit := range parseUnitList(string(out)) {
		pidOut, err := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", unit).Output()
		if err != nil {
			return nil, nil, fmt.Errorf("systemctl show %s: %w", unit, err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(pidOut)))
		if err != nil || pid <= 0 {
			return nil, nil, fmt.Errorf("%s has no main process", unit)
		}
		s, err := inspectProcess(unit, filepath.Join("/proc", strconv.Itoa(pid)))
		if err != nil {
			return nil, nil, err
		}
		if s.Exe == target {
			mine = append(mine, s)
		} else {
			others = append(others, s)
		}
	}
	return mine, others, nil
}

func parseUnitList(out string) []string {
	var units []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) > 0 && strings.HasPrefix(f[0], "bento@") && strings.HasSuffix(f[0], ".service") {
			units = append(units, f[0])
		}
	}
	return units
}

// inspectProcess reads a backend's binary, stack root and utils listeners
// from its /proc directory.
func inspectProcess(unit, proc string) (service, error) {
	s := service{Unit: unit}
	exe, err := os.Readlink(filepath.Join(proc, "exe"))
	if err != nil {
		return s, fmt.Errorf("%s: %w", unit, err)
	}
	s.Exe = strings.TrimSuffix(exe, " (deleted)")
	env, err := os.ReadFile(filepath.Join(proc, "environ"))
	if err != nil {
		return s, fmt.Errorf("%s: %w", unit, err)
	}
	for kv := range strings.SplitSeq(string(env), "\x00") {
		if v, ok := strings.CutPrefix(kv, "BENTO_STACK_ROOT="); ok {
			s.Root = v
		}
	}
	cmdline, err := os.ReadFile(filepath.Join(proc, "cmdline"))
	if err != nil {
		return s, fmt.Errorf("%s: %w", unit, err)
	}
	argv := strings.Split(strings.TrimSuffix(string(cmdline), "\x00"), "\x00")
	for i := 1; i < len(argv); i++ {
		name, val, hasVal := strings.Cut(strings.TrimLeft(argv[i], "-"), "=")
		if !strings.HasPrefix(argv[i], "-") || (name != "stack" && name != "utils-listen") {
			continue
		}
		if !hasVal {
			if i+1 >= len(argv) {
				break
			}
			i++
			val = argv[i]
		}
		if name == "stack" {
			s.Root = val
		} else {
			s.UtilsListen = append(s.UtilsListen, val)
		}
	}
	if s.Root == "" {
		return s, fmt.Errorf("%s: no stack root in its command line or environment", unit)
	}
	return s, nil
}
