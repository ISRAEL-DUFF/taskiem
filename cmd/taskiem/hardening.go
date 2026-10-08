package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/egress"
)

// defaultHSTS is sent when TASKIEM_PUBLIC_URL is https and TASKIEM_HSTS is
// not set: a year, without includeSubDomains (other hosts under the
// platform's domain are not Taskiem's to commit) or preload.
const defaultHSTS = "max-age=31536000"

// hstsFromEnv is the Strict-Transport-Security value (self-review S35):
// TASKIEM_HSTS as given (it must start with max-age=), "off" for none, and
// by default defaultHSTS when the public URL is https.
func hstsFromEnv(lookup func(string) (string, bool), publicURL string) (string, error) {
	v, ok := lookup("TASKIEM_HSTS")
	v = strings.TrimSpace(v)
	switch {
	case !ok || v == "":
		if strings.HasPrefix(strings.ToLower(publicURL), "https://") {
			return defaultHSTS, nil
		}
		return "", nil
	case strings.EqualFold(v, "off") || strings.EqualFold(v, "false") || v == "0":
		return "", nil
	case strings.HasPrefix(strings.ToLower(v), "max-age="):
		return v, nil
	}
	return "", fmt.Errorf("TASKIEM_HSTS: %q is neither off nor a header value starting with max-age=", v)
}

// codeLimitsFromEnv bounds tenant code compiled or checked in the API
// process (self-review S34): TASKIEM_TENANT_CODE_CONCURRENCY (default the
// CPUs, at least 2) and TASKIEM_TENANT_CODE_PER_TENANT (default half of
// it, at least 1).
func codeLimitsFromEnv(lookup func(string) (string, bool)) (api.CodeLimits, error) {
	var l api.CodeLimits
	for _, f := range []struct {
		name string
		to   *int
	}{{"TASKIEM_TENANT_CODE_CONCURRENCY", &l.Concurrency}, {"TASKIEM_TENANT_CODE_PER_TENANT", &l.PerTenant}} {
		v, ok := lookup(f.name)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return l, fmt.Errorf("%s: %q is not a positive number", f.name, v)
		}
		*f.to = n
	}
	return l, nil
}

// harden applies the API's hardening settings from the environment.
func harden(srv *api.Server, publicURL string) error {
	hsts, err := hstsFromEnv(os.LookupEnv, publicURL)
	if err != nil {
		return err
	}
	limits, err := codeLimitsFromEnv(os.LookupEnv)
	if err != nil {
		return err
	}
	srv.HSTS, srv.CodeLimits = hsts, limits
	return nil
}

// configureEgress sets the explicit egress proxy tenant traffic leaves
// through (TASKIEM_EGRESS_PROXY, decision 0028), or none. A proxy set
// badly stops the process: tenant traffic must never leave by a route the
// operator did not name.
func configureEgress(log *slog.Logger) error {
	up, err := egress.UpstreamFromEnv(os.LookupEnv)
	if err != nil {
		return err
	}
	egress.SetUpstream(up)
	if up != nil && log != nil {
		log.Info("tenant traffic leaves through the egress proxy", "proxy", up.String(), "authenticated", up.User != "")
	}
	return nil
}
