package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/israel-duff/taskiem/engine/push"
)

// pushCmd is `taskiem push keys`: a VAPID key pair for chat push
// (docs/spec/chat-push.md), printed as the settings to put in the operator's
// secrets. A pair is made once for a deployment: changing it ends every
// browser's subscription until each person turns notifications on again.
func pushCmd(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "keys" {
		return fmt.Errorf("usage: taskiem push keys [--subject mailto:ops@example.com]")
	}
	fs := flag.NewFlagSet("push keys", flag.ContinueOnError)
	subject := fs.String("subject", "mailto:ops@example.com", "a mailto: or https: address the push services may reach (RFC 8292)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	private, public, err := push.GenerateKeys()
	if err != nil {
		return fmt.Errorf("push keys: %w", err)
	}
	cfg := push.Config{PublicKey: public, PrivateKey: private, Subject: *subject}
	if err := cfg.Validate(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "TASKIEM_VAPID_PUBLIC_KEY=%s\nTASKIEM_VAPID_PRIVATE_KEY=%s\nTASKIEM_VAPID_SUBJECT=%s\n", public, private, *subject)
	fmt.Fprintln(stdout, "# Keep the private key secret. Make this pair once: changing it ends every browser's notifications until each person turns them on again.")
	return nil
}
