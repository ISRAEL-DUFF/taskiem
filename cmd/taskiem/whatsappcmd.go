package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

const whatsappUsage = `usage: taskiem whatsapp templates [list | submit NAME|all | check]

The platform number's WhatsApp templates (docs/whatsapp.md#templates), in
its WhatsApp Business Account (TASKIEM_WHATSAPP_WABA_ID):
  list     each template's status in the registry (the default)
  submit   submit one template (or all not yet submitted) to Meta for approval
  check    ask Meta for every template's status now and record it`

// whatsappHosts are the addresses the templates' url buttons open: the web
// app (TASKIEM_PUBLIC_URL) and the public form host.
func whatsappHosts(cfg config) whatsapp.Hosts {
	h := whatsapp.Hosts{App: cfg.PublicURL}
	if fh := formHost(cfg); fh != "" {
		h.Forms = "https://" + fh
	}
	return h
}

// whatsappCmd is the operator's view of the platform number's templates.
func whatsappCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "templates" {
		return errors.New(whatsappUsage)
	}
	sub := "list"
	if len(args) > 1 {
		sub = args[1]
	}
	switch sub {
	case "list", "check":
		if len(args) > 2 {
			return errors.New(whatsappUsage)
		}
	case "submit":
		if len(args) != 3 {
			return errors.New(whatsappUsage)
		}
	default:
		return errors.New(whatsappUsage)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("whatsapp templates: %w", err)
	}
	if cfg.WhatsApp == nil {
		return errors.New("whatsapp templates: the platform number is not configured (TASKIEM_WHATSAPP_PHONE_NUMBER_ID)")
	}
	if cfg.WhatsApp.WABAID == "" {
		return errors.New("whatsapp templates: TASKIEM_WHATSAPP_WABA_ID is not set, so the platform number's templates cannot be tracked")
	}
	cfg.WhatsApp.Hosts = whatsappHosts(cfg)
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("whatsapp templates: %w", err)
	}
	defer pool.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	wa := whatsapp.New(pool, *cfg.WhatsApp, &egress.Guard{Logger: log}, log)
	switch sub {
	case "submit":
		names := []string{args[2]}
		if args[2] == "all" {
			names = nil
			all, err := wa.TemplateStatuses(ctx)
			if err != nil {
				return fmt.Errorf("whatsapp templates: %w", err)
			}
			for _, s := range all {
				if s.Status == whatsapp.TemplateNotSubmitted {
					names = append(names, s.Name)
				}
			}
			if len(names) == 0 {
				fmt.Fprintln(stdout, "every template is submitted already")
			}
		}
		for _, name := range names {
			st, err := wa.SubmitTemplate(ctx, name)
			if err != nil {
				return fmt.Errorf("whatsapp templates: submitting %s: %w", name, err)
			}
			fmt.Fprintf(stdout, "submitted %s: %s (Meta id %s)\n", st.Name, st.Status, st.MetaID)
		}
	case "check":
		if _, err := wa.CheckTemplates(ctx); err != nil {
			return fmt.Errorf("whatsapp templates: %w", err)
		}
		fmt.Fprintln(stdout, "checked with Meta")
	}
	statuses, err := wa.TemplateStatuses(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp templates: %w", err)
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TEMPLATE\tCATEGORY\tSTATUS\tSUBMITTED\tCHECKED\tREASON")
	at := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.UTC().Format("2006-01-02 15:04")
	}
	for _, s := range statuses {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Category, strings.ReplaceAll(s.Status, "_", " "), at(s.SubmittedAt), at(s.CheckedAt), s.Reason)
	}
	_ = tw.Flush()
	if note := whatsapp.NotApprovedNote(statuses); note != "" {
		fmt.Fprintln(stdout, "\n"+note)
	}
	return nil
}
