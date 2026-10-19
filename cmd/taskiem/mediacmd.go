package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/secrets"
)

const mediaUsage = "usage: taskiem media check TENANT_ID"

// mediaCmd is `taskiem media check TENANT_ID`: a round trip through the
// tenant's Xenibase storage (docs/media.md). It makes the buckets, stores
// a generated photo and downloads it from its public URL, stores a PDF
// and downloads it through a signed URL, then deletes both. Run it once
// per new Xenibase release or install, before storefronts rely on it.
func mediaCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 2 || args[0] != "check" {
		return errors.New(mediaUsage)
	}
	tenant, err := uuid.Parse(args[1])
	if err != nil {
		return errors.New(mediaUsage)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	kms, err := cfg.kms()
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := configureEgress(nil); err != nil {
		return err
	}
	guard := &egress.Guard{Logger: log}
	v := &secrets.Vault{Pool: pool, KMS: kms, RootKey: cfg.KMSKey, BYOK: &byok.Factory{Guard: guard, AllowPrivate: cfg.Keys.AllowPrivate}}
	tables := &datatables.Service{Pool: pool, Vault: v, Egress: guard, Logger: log, Platform: cfg.Xenibase, ConnectorURL: cfg.Connectors.XenibaseURL}
	m := &media.Service{Credentials: tables.Credentials, Guard: guard, Logger: log}

	step := func(name string, err error) error {
		if err != nil {
			fmt.Fprintf(stdout, "FAIL  %s: %v\n", name, err)
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Fprintf(stdout, "ok    %s\n", name)
		return nil
	}
	img, err := m.PutImage(ctx, tenant, testPhoto())
	if err := step("buckets ready and photo stored at "+fmt.Sprint(len(img.Paths))+" sizes", err); err != nil {
		return err
	}
	defer func() { _ = m.Delete(context.WithoutCancel(ctx), tenant, media.PublicBucket, img.Paths) }()
	if err := step("photo downloads from its public URL", fetch(ctx, guard, img.URLs[img.Width], "image/jpeg")); err != nil {
		return err
	}
	file, err := m.PutFile(ctx, tenant, []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n"))
	if err := step("private file stored", err); err != nil {
		return err
	}
	defer func() { _ = m.Delete(context.WithoutCancel(ctx), tenant, media.PrivateBucket, []string{file.Path}) }()
	signed, _, err := m.SignURL(ctx, tenant, file.Path, 5*time.Minute)
	if err := step("signed URL made", err); err != nil {
		return err
	}
	if err := step("private file downloads through it", fetch(ctx, guard, signed, "")); err != nil {
		return err
	}
	if err := step("private file is not public", notFetchable(ctx, guard, publicBase(img.URLs[img.Width])+"/storage/v1/public/"+media.PrivateBucket+"/"+file.Path)); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "storage works; the test objects are deleted")
	return nil
}

func testPhoto() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 640, 320))
	for y := range 320 {
		for x := range 640 {
			img.Set(x, y, color.RGBA{R: uint8(x / 3), G: uint8(y / 2), B: 120, A: 255}) //nolint:gosec // bounded by the loops
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

func fetch(ctx context.Context, g *egress.Guard, raw, wantType string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	c := g.Client(egress.Policy{Tenant: "platform", Hosts: []string{u.Hostname()}, Purpose: "media:check"}, 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil) //nolint:gosec // a URL of the tenant's project, fetched through the egress guard
	if err != nil {
		return err
	}
	resp, err := c.Do(req) //nolint:gosec // as above: the guard refuses private addresses
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, 30<<20))
	if resp.StatusCode != http.StatusOK || n == 0 {
		return fmt.Errorf("GET answered %d with %d bytes", resp.StatusCode, n)
	}
	if wantType != "" && resp.Header.Get("Content-Type") != wantType {
		return fmt.Errorf("served as %q, not %s", resp.Header.Get("Content-Type"), wantType)
	}
	return nil
}

func notFetchable(ctx context.Context, g *egress.Guard, raw string) error {
	if err := fetch(ctx, g, raw, ""); err == nil {
		return errors.New("the private file downloaded from a public URL")
	}
	return nil
}

// publicBase is the project host of a public URL.
func publicBase(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
