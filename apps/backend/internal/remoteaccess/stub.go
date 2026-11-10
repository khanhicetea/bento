package remoteaccess

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// TestChildArg is the argument with which a test binary runs RunTestChild
// (see the TestMain functions that call MaybeRunTestChild).
const TestChildArg = "bento-tailcat-test-child"

// TestRealChildArg starts the real child (RunChild) from a test binary, for
// tests that reach the Tailcat relays.
const TestRealChildArg = "bento-tailcat-test-real-child"

// StubRegion is the DERP region the test child claims to listen in.
const StubRegion = 901

// MaybeRunTestChild runs RunTestChild and exits when the test binary was
// started as a stub child. It is a no-op otherwise.
func MaybeRunTestChild() {
	if len(os.Args) > 1 && os.Args[1] == TestRealChildArg {
		if err := RunChild(); err != nil {
			fmt.Fprintln(os.Stderr, "tailcat:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == TestChildArg {
		if err := RunTestChild(); err != nil {
			fmt.Fprintln(os.Stderr, "tailcat stub:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// RunTestChild speaks the child protocol without any network: it checks the
// same privilege rules, reads the key from stdin, reports the address the
// key gives in StubRegion (or its recorded region) and waits for SIGTERM.
// Tests use it to exercise supervision and the operation flow.
func RunTestChild() error {
	if err := dropPrivilegeLeftovers(); err != nil {
		return err
	}
	cfg, err := readChildConfig(os.Stdin)
	if err != nil {
		return err
	}
	if err := syscall.Fchdir(childSocketFD); err != nil {
		return fmt.Errorf("enter socket directory: %w", err)
	}
	region := cfg.RegionID
	if region == 0 {
		region = StubRegion
	}
	k := Key{Version: keyFileVersion, Private: cfg.Private, PresharedKey: cfg.PresharedKey, RegionID: region}
	addr, err := k.Address()
	if err != nil {
		return err
	}
	line, _ := json.Marshal(childReport{Address: string(addr), RegionID: region})
	fmt.Fprintf(os.Stdout, "%s\n", line)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	return nil
}
