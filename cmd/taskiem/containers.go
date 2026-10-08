package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/container"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// containerSetup equips the container queue's workers (docs/container-steps.md):
// the runner chosen by TASKIEM_CONTAINER_RUNNER and the egress proxy for
// steps with network "egress". It returns the tasks to run beside the
// workers (the proxy's listener).
//
//	TASKIEM_CONTAINER_RUNNER             kubernetes | local (development only)
//	TASKIEM_CONTAINER_NAMESPACE          the sandbox namespace (kubernetes)
//	TASKIEM_CONTAINER_RUNTIME_CLASS      default gvisor
//	TASKIEM_CONTAINER_REGISTRIES         allowed image prefixes, comma-separated
//	TASKIEM_CONTAINER_SHIM_IMAGE         image holding /taskiem-shim (the Taskiem image)
//	TASKIEM_CONTAINER_IMAGE_PULL_SECRETS Secrets in the sandbox namespace, comma-separated
//	TASKIEM_CONTAINER_NODE_SELECTOR      key=value,... for sandbox Pods
//	TASKIEM_CONTAINER_NETWORK_POLICY     must exist in the namespace; default taskiem-sandbox
//	TASKIEM_CONTAINER_PROXY_LISTEN       the egress proxy's listener; default :3128
//	TASKIEM_CONTAINER_PROXY_ADDR         host:port sandboxes reach it at (the worker Pod's IP)
func containerSetup(ctx context.Context, w *runtime.Worker, log *slog.Logger) ([]func(context.Context) error, error) {
	var runner container.Runner
	switch kind := os.Getenv(container.EnvRunner); kind {
	case "":
		log.Warn("container queue without TASKIEM_CONTAINER_RUNNER: container steps will fail as not enabled")
		return nil, nil
	case "local":
		l, err := container.NewLocal(os.Getenv, log)
		if err != nil {
			return nil, err
		}
		runner = l
	case "kubernetes":
		k := &container.Kubernetes{
			Namespace:        os.Getenv("TASKIEM_CONTAINER_NAMESPACE"),
			RuntimeClass:     os.Getenv("TASKIEM_CONTAINER_RUNTIME_CLASS"),
			Registries:       splitList(os.Getenv("TASKIEM_CONTAINER_REGISTRIES")),
			ShimImage:        os.Getenv("TASKIEM_CONTAINER_SHIM_IMAGE"),
			ImagePullSecrets: splitList(os.Getenv("TASKIEM_CONTAINER_IMAGE_PULL_SECRETS")),
			NetworkPolicy:    os.Getenv("TASKIEM_CONTAINER_NETWORK_POLICY"),
			Labels:           map[string]string{"taskiem.dev/worker": w.ID},
			Logger:           log,
		}
		for _, kv := range splitList(os.Getenv("TASKIEM_CONTAINER_NODE_SELECTOR")) {
			key, v, ok := strings.Cut(kv, "=")
			if !ok {
				return nil, fmt.Errorf("TASKIEM_CONTAINER_NODE_SELECTOR: %q is not key=value", kv)
			}
			if k.NodeSelector == nil {
				k.NodeSelector = map[string]string{}
			}
			k.NodeSelector[key] = v
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := k.Check(cctx); err != nil {
			return nil, err
		}
		runner = k
	default:
		return nil, fmt.Errorf("%s: unknown runner %q (kubernetes or local)", container.EnvRunner, kind)
	}
	w.Containers = runner

	listen := env("TASKIEM_CONTAINER_PROXY_LISTEN", ":3128")
	addr := os.Getenv("TASKIEM_CONTAINER_PROXY_ADDR")
	if addr == "" {
		if _, port, err := net.SplitHostPort(listen); err == nil && os.Getenv(container.EnvRunner) == "local" {
			addr = net.JoinHostPort("127.0.0.1", port)
		}
	}
	if addr == "" {
		log.Warn("TASKIEM_CONTAINER_PROXY_ADDR unset: container steps with network egress will fail")
		return nil, nil
	}
	w.Proxy = &egress.Proxy{Guard: w.Egress, Logger: log}
	w.ProxyAddr = addr
	return []func(context.Context) error{proxyTask(listen, w.Proxy, log)}, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// proxyTask serves the egress proxy. Tunnels are long-lived, so the server
// sets no read or write timeout; the proxy closes idle tunnels itself and
// every tunnel when its step's grant is revoked.
func proxyTask(addr string, p *egress.Proxy, log *slog.Logger) func(context.Context) error {
	return func(ctx context.Context) error {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("egress proxy: %w", err)
		}
		srv := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
		log.Info("listening", "server", "egress-proxy", "addr", ln.Addr().String())
		done := make(chan error, 1)
		go func() { done <- srv.Serve(ln) }()
		select {
		case err := <-done:
			return fmt.Errorf("egress proxy: %w", err)
		case <-ctx.Done():
		}
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}
