package container

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

// Kubernetes runs each attempt as a Pod in a dedicated namespace (spec
// 7.5, boundary B14 in docs/security/threat-model.md). It talks to the API
// server directly over HTTPS with the worker's in-cluster service account;
// the account's Role allows pods, pods/log and secrets in that namespace
// only (deploy/helm/taskiem/templates/containersteps.yaml).
//
// Every Pod runs under RuntimeClass (gVisor's runsc by default), as a
// non-root user with a read-only root file system, every capability
// dropped, no privilege escalation, the runtime's seccomp profile, no
// service account token, no service links, guaranteed CPU and memory, and
// activeDeadlineSeconds. The namespace's NetworkPolicy (Check verifies it
// exists) blocks all traffic but the worker's egress proxy; a step with
// network "none" is not given the proxy's address or a token either. The
// step's input, declared secrets and proxy credentials travel in a Secret
// owned by the Pod, so deleting the Pod removes them; the Pod is deleted
// when the attempt ends, whatever happened.
type Kubernetes struct {
	// APIServer defaults to https://$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT.
	APIServer string
	// TokenFile and CAFile default to the service account's mounted files.
	// The token is re-read for each request: projected tokens rotate.
	TokenFile string
	CAFile    string
	// HTTP replaces the client built from CAFile (tests).
	HTTP *http.Client

	Namespace string
	// RuntimeClass names the sandboxing runtime; default "gvisor".
	RuntimeClass string
	// Registries are the image prefixes allowed ("registry.example.com",
	// "ghcr.io/acme"); an image elsewhere is refused. Empty refuses all.
	Registries []string
	// ShimImage holds /taskiem-shim (the Taskiem image does).
	ShimImage        string
	ImagePullSecrets []string
	NodeSelector     map[string]string
	// NetworkPolicy must exist in Namespace (Check); default "taskiem-sandbox".
	NetworkPolicy string
	// StartTimeout bounds scheduling and image pulls; default 2m.
	StartTimeout time.Duration
	// Poll is how often a Pod's status is read; default 1s.
	Poll time.Duration
	// Labels are added to every Pod (the worker's identity).
	Labels map[string]string
	Logger *slog.Logger
}

// Defaults for the Kubernetes runner.
const (
	DefaultRuntimeClass  = "gvisor"
	DefaultNetworkPolicy = "taskiem-sandbox"
	// SandboxLabel marks Pods the runner made (and sweeps).
	SandboxLabel = "taskiem.dev/sandbox"
	// maxLifetime bounds any sandbox Pod: start, the longest step timeout
	// (30m, wd.MaxContainerTimeout) and slack. Older ones are swept.
	maxLifetime = 40 * time.Minute
)

const (
	saDir = "/var/run/secrets/kubernetes.io/serviceaccount"
)

func (k *Kubernetes) logger() *slog.Logger {
	if k.Logger == nil {
		return slog.Default()
	}
	return k.Logger
}

func (k *Kubernetes) runtimeClass() string {
	if k.RuntimeClass == "" {
		return DefaultRuntimeClass
	}
	return k.RuntimeClass
}

func (k *Kubernetes) startTimeout() time.Duration {
	if k.StartTimeout <= 0 {
		return 2 * time.Minute
	}
	return k.StartTimeout
}

func (k *Kubernetes) poll() time.Duration {
	if k.Poll <= 0 {
		return time.Second
	}
	return k.Poll
}

func (k *Kubernetes) server() string {
	if k.APIServer != "" {
		return strings.TrimRight(k.APIServer, "/")
	}
	return "https://" + net.JoinHostPort(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"))
}

func (k *Kubernetes) client() (*http.Client, error) {
	if k.HTTP != nil {
		return k.HTTP, nil
	}
	caFile := k.CAFile
	if caFile == "" {
		caFile = saDir + "/ca.crt"
	}
	pem, err := os.ReadFile(caFile) //nolint:gosec // the operator's configured CA file
	if err != nil {
		return nil, fmt.Errorf("kubernetes CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("kubernetes CA: no certificates")
	}
	// The API server is in the cluster: no proxy from the environment.
	k.HTTP = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		MaxIdleConns: 4, IdleConnTimeout: 90 * time.Second,
	}}
	return k.HTTP, nil
}

func (k *Kubernetes) token() (string, error) {
	f := k.TokenFile
	if f == "" {
		f = saDir + "/token"
	}
	b, err := os.ReadFile(f) //nolint:gosec // the service account's token file
	if err != nil {
		return "", fmt.Errorf("kubernetes token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// apiError is a non-2xx answer from the API server.
type apiError struct {
	Code    int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("kubernetes API: %d %s", e.Code, e.Message) }

// do sends one request; out (if any) receives a 2xx JSON body, raw a text
// one. A non-2xx answer is an *apiError.
func (k *Kubernetes) do(ctx context.Context, method, path string, body any, out any) error {
	hc, err := k.client()
	if err != nil {
		return err
	}
	tok, err := k.token()
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.server()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var st struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &st)
		if st.Message == "" {
			st.Message = http.StatusText(resp.StatusCode)
		}
		return &apiError{Code: resp.StatusCode, Message: st.Message}
	}
	switch o := out.(type) {
	case nil:
	case *string:
		*o = string(raw)
	default:
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (k *Kubernetes) nsPath(kind string) string {
	return "/api/v1/namespaces/" + url.PathEscape(k.Namespace) + "/" + kind
}

// Check verifies the namespace's NetworkPolicy exists, so no Pod ever runs
// with the cluster's default (open) network. The worker refuses to start
// the container queue without it.
func (k *Kubernetes) Check(ctx context.Context) error {
	if k.Namespace == "" {
		return errors.New("container runner: no namespace")
	}
	if len(k.Registries) == 0 {
		return errors.New("container runner: no registries allowed (TASKIEM_CONTAINER_REGISTRIES)")
	}
	if k.ShimImage == "" {
		return errors.New("container runner: no shim image (TASKIEM_CONTAINER_SHIM_IMAGE)")
	}
	np := k.NetworkPolicy
	if np == "" {
		np = DefaultNetworkPolicy
	}
	err := k.do(ctx, http.MethodGet, "/apis/networking.k8s.io/v1/namespaces/"+url.PathEscape(k.Namespace)+"/networkpolicies/"+url.PathEscape(np), nil, &map[string]any{})
	if err != nil {
		return fmt.Errorf("container runner: NetworkPolicy %s/%s: %w", k.Namespace, np, err)
	}
	return nil
}

var digestRe = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

// ImageAllowed reports whether an image is pinned and on the registry
// allow-list.
func (k *Kubernetes) ImageAllowed(image string) bool {
	if !digestRe.MatchString(image) {
		return false
	}
	repo, _, _ := strings.Cut(image, "@")
	if strings.Contains(repo[strings.LastIndex(repo, "/")+1:], ":") {
		return false // a tag
	}
	for _, r := range k.Registries {
		r = strings.TrimRight(r, "/")
		if r != "" && strings.HasPrefix(repo, r+"/") {
			return true
		}
	}
	return false
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// labelValue keeps a label value valid (63 characters of [A-Za-z0-9_.-]).
func labelValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, s)
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.Trim(s, "-_.")
}

func secretKey(name string) string { return "secret." + name }

// Pod is the Pod an attempt runs in (exported for tests and docs).
func (k *Kubernetes) Pod(s Spec) map[string]any {
	name := "tsk-" + s.Name
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "taskiem",
		"app.kubernetes.io/component":  "container-step",
		SandboxLabel:                   "true",
		"taskiem.dev/tenant":           labelValue(s.Tenant),
		"taskiem.dev/run":              labelValue(s.Run),
	}
	for k, v := range k.Labels {
		labels[k] = labelValue(v)
	}
	inputMode, outputMode, secretsMode := orDefault(s.InputMode, "stdin"), orDefault(s.OutputMode, "stdout"), orDefault(s.SecretsMode, "env")
	timeout := max(s.Timeout, time.Second)
	command := []string{ShimPath, "run", "--input-mode", inputMode, "--output-mode", outputMode,
		"--output-bytes", strconv.Itoa(s.OutputBytes), "--timeout", timeout.String(), "--"}
	command = append(append(command, s.Command...), s.Args...)

	env := []map[string]any{
		{"name": "TASKIEM_INPUT", "value": InputPath},
		{"name": "TASKIEM_OUTPUT", "value": OutputPath},
		{"name": "HOME", "value": "/tmp"},
		{"name": "TMPDIR", "value": "/tmp"},
	}
	for _, k := range sortedKeys(s.Env) {
		env = append(env, map[string]any{"name": k, "value": s.Env[k]})
	}
	ref := func(key string) map[string]any {
		return map[string]any{"secretKeyRef": map[string]any{"name": name, "key": key}}
	}
	if s.Proxy != "" {
		for _, v := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"} {
			env = append(env, map[string]any{"name": v, "valueFrom": ref("proxy")})
		}
	}
	mounts := []map[string]any{
		{"name": "shim", "mountPath": ShimDir, "readOnly": true},
		{"name": "input", "mountPath": "/taskiem/in", "readOnly": true},
		{"name": "out", "mountPath": "/taskiem/out"},
		{"name": "tmp", "mountPath": "/tmp"},
	}
	inputItems := []map[string]any{{"key": "input.json", "path": "input.json"}}
	volumes := []map[string]any{
		{"name": "shim", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": "64Mi"}},
		{"name": "input", "secret": map[string]any{"secretName": name, "defaultMode": 0o440, "items": inputItems}},
		{"name": "out", "emptyDir": map[string]any{"sizeLimit": fmt.Sprintf("%dMi", max(1, 2*s.OutputBytes>>20))}},
		{"name": "tmp", "emptyDir": map[string]any{"sizeLimit": "256Mi"}},
	}
	names := sortedKeys(s.Secrets)
	if secretsMode == "file" && len(names) > 0 {
		var items []map[string]any
		for _, n := range names {
			items = append(items, map[string]any{"key": secretKey(n), "path": n})
		}
		volumes = append(volumes, map[string]any{"name": "secrets", "secret": map[string]any{"secretName": name, "defaultMode": 0o440, "items": items}})
		mounts = append(mounts, map[string]any{"name": "secrets", "mountPath": SecretsPath, "readOnly": true})
		env = append(env, map[string]any{"name": "TASKIEM_SECRETS", "value": SecretsPath})
	} else {
		for _, n := range names {
			env = append(env, map[string]any{"name": n, "valueFrom": ref(secretKey(n))})
		}
	}
	containerSecurity := map[string]any{
		"allowPrivilegeEscalation": false,
		"readOnlyRootFilesystem":   true,
		"privileged":               false,
		"runAsNonRoot":             true,
		"capabilities":             map[string]any{"drop": []string{"ALL"}},
		"seccompProfile":           map[string]any{"type": "RuntimeDefault"},
	}
	cpu, mem := fmt.Sprintf("%dm", max(s.CPUMillis, 100)), fmt.Sprintf("%dMi", max(s.MemoryMB, 32))
	resources := map[string]any{
		"requests": map[string]any{"cpu": cpu, "memory": mem, "ephemeral-storage": "64Mi"},
		"limits":   map[string]any{"cpu": cpu, "memory": mem, "ephemeral-storage": "512Mi"},
	}
	spec := map[string]any{
		"runtimeClassName":              k.runtimeClass(),
		"restartPolicy":                 "Never",
		"automountServiceAccountToken":  false,
		"enableServiceLinks":            false,
		"hostNetwork":                   false,
		"hostPID":                       false,
		"hostIPC":                       false,
		"shareProcessNamespace":         false,
		"terminationGracePeriodSeconds": 1,
		"activeDeadlineSeconds":         int64((k.startTimeout() + timeout + 30*time.Second).Seconds()),
		// No resolver: names are resolved by the egress proxy, never here.
		"dnsPolicy": "None",
		"dnsConfig": map[string]any{"nameservers": []string{"127.0.0.1"}},
		"securityContext": map[string]any{
			"runAsNonRoot":   true,
			"runAsUser":      65532,
			"runAsGroup":     65532,
			"fsGroup":        65532,
			"seccompProfile": map[string]any{"type": "RuntimeDefault"},
		},
		"initContainers": []map[string]any{{
			"name": "shim", "image": k.ShimImage, "imagePullPolicy": "IfNotPresent",
			"command":         []string{"/taskiem-shim", "install", ShimPath},
			"securityContext": containerSecurity,
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "50m", "memory": "32Mi"},
				"limits":   map[string]any{"cpu": "200m", "memory": "64Mi"},
			},
			"volumeMounts": []map[string]any{{"name": "shim", "mountPath": ShimDir}},
		}},
		"containers": []map[string]any{{
			"name": "main", "image": s.Image, "imagePullPolicy": "IfNotPresent",
			"command": command, "env": env, "volumeMounts": mounts,
			"securityContext": containerSecurity, "resources": resources,
			"terminationMessagePolicy": "File",
		}},
		"volumes": volumes,
	}
	if len(k.ImagePullSecrets) > 0 {
		var ps []map[string]any
		for _, n := range k.ImagePullSecrets {
			ps = append(ps, map[string]any{"name": n})
		}
		spec["imagePullSecrets"] = ps
	}
	if len(k.NodeSelector) > 0 {
		spec["nodeSelector"] = k.NodeSelector
	}
	return map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{
			"name": name, "namespace": k.Namespace, "labels": labels,
			"annotations": map[string]string{"taskiem.dev/step": s.Step, "taskiem.dev/attempt": strconv.Itoa(s.Attempt), "taskiem.dev/image": s.Image},
		},
		"spec": spec,
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// podStatus is the part of a Pod's status the runner reads.
type podStatus struct {
	Metadata struct {
		Name              string    `json:"name"`
		UID               string    `json:"uid"`
		CreationTimestamp time.Time `json:"creationTimestamp"`
	} `json:"metadata"`
	Status struct {
		Phase                 string            `json:"phase"`
		Reason                string            `json:"reason"`
		Message               string            `json:"message"`
		InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
		ContainerStatuses     []containerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type containerStatus struct {
	Name  string `json:"name"`
	State struct {
		Waiting *struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"waiting"`
		Running *struct {
			StartedAt time.Time `json:"startedAt"`
		} `json:"running"`
		Terminated *struct {
			ExitCode   int       `json:"exitCode"`
			Reason     string    `json:"reason"`
			Message    string    `json:"message"`
			StartedAt  time.Time `json:"startedAt"`
			FinishedAt time.Time `json:"finishedAt"`
		} `json:"terminated"`
	} `json:"state"`
}

func (p *podStatus) main() *containerStatus {
	for i := range p.Status.ContainerStatuses {
		if p.Status.ContainerStatuses[i].Name == "main" {
			return &p.Status.ContainerStatuses[i]
		}
	}
	return nil
}

// stuck reports a reason the Pod will never start its program.
func (p *podStatus) stuck() string {
	for _, cs := range append(append([]containerStatus(nil), p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "ErrImageNeverPull", "CreateContainerConfigError", "CreateContainerError", "RunContainerError":
				return fmt.Sprintf("%s: %s: %s", cs.Name, w.Reason, w.Message)
			}
		}
		if cs.Name == "shim" {
			if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
				return fmt.Sprintf("shim: exit %d %s", t.ExitCode, t.Reason)
			}
		}
	}
	return ""
}

// Run creates the attempt's Pod and Secret, waits for the program to end,
// reads its result from the log and deletes both.
func (k *Kubernetes) Run(ctx context.Context, s Spec) (Result, error) {
	if !nameRe.MatchString(s.Name) {
		return Result{}, fatal("container run name %q is not valid", s.Name)
	}
	if !k.ImageAllowed(s.Image) {
		return Result{}, fmt.Errorf("image %s is not from an allowed registry (%s): %w: %w", s.Image, strings.Join(k.Registries, ", "), ErrRefused, effects.ErrFatal)
	}
	name := "tsk-" + s.Name
	var created podStatus
	if err := k.do(ctx, http.MethodPost, k.nsPath("pods"), k.Pod(s), &created); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Code/100 == 4 && ae.Code != http.StatusConflict && ae.Code != http.StatusTooManyRequests {
			// Refused by admission (pod security, a missing RuntimeClass,
			// quota): sending it again will not help.
			return Result{}, fatal("container Pod refused: %v", err)
		}
		return Result{}, notStarted("create Pod: %v", err)
	}
	defer k.remove(name)

	data := map[string]string{"input.json": string(s.Input)}
	for n, v := range s.Secrets {
		data[secretKey(n)] = v
	}
	if s.Proxy != "" {
		data["proxy"] = s.Proxy
	}
	secret := map[string]any{
		"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata": map[string]any{
			"name": name, "namespace": k.Namespace,
			"labels": map[string]string{"app.kubernetes.io/managed-by": "taskiem", SandboxLabel: "true"},
			// Owned by the Pod: deleting it (or the sweep) deletes this.
			"ownerReferences": []map[string]any{{"apiVersion": "v1", "kind": "Pod", "name": name, "uid": created.Metadata.UID}},
		},
		"stringData": data,
	}
	if err := k.do(ctx, http.MethodPost, k.nsPath("secrets"), secret, nil); err != nil {
		return Result{}, notStarted("create Secret: %v", err)
	}

	start := time.Now()
	running := false
	var p podStatus
	for {
		select {
		case <-ctx.Done():
			if running {
				return Result{}, unknown("container step cancelled while running")
			}
			return Result{}, notStarted("cancelled before the program started")
		case <-time.After(k.poll()):
		}
		p = podStatus{}
		if err := k.do(ctx, http.MethodGet, k.nsPath("pods")+"/"+name, nil, &p); err != nil {
			if ctx.Err() != nil {
				continue
			}
			var ae *apiError
			if errors.As(err, &ae) && ae.Code == http.StatusNotFound {
				if running {
					return Result{}, unknown("container Pod disappeared while running")
				}
				return Result{}, notStarted("container Pod disappeared before it started")
			}
			k.logger().Warn("container runner: reading Pod status", "pod", name, "err", err)
			continue
		}
		m := p.main()
		if m != nil && (m.State.Running != nil || m.State.Terminated != nil) {
			running = true
		}
		if p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			break
		}
		if !running {
			if why := p.stuck(); why != "" {
				return Result{}, notStarted("%s", why)
			}
			if time.Since(start) > k.startTimeout() {
				return Result{}, notStarted("not running after %s (%s)", k.startTimeout(), p.Status.Phase)
			}
		}
	}
	m := p.main()
	if m == nil || m.State.Terminated == nil {
		if p.Status.Reason == "DeadlineExceeded" && running {
			return Result{}, unknown("container step timed out")
		}
		if !running {
			if why := p.stuck(); why != "" {
				return Result{}, notStarted("%s", why)
			}
			return Result{}, notStarted("Pod %s: %s %s", p.Status.Phase, p.Status.Reason, p.Status.Message)
		}
		return Result{}, unknown("container Pod ended (%s %s) without a result", p.Status.Reason, p.Status.Message)
	}
	t := m.State.Terminated
	elapsed := t.FinishedAt.Sub(t.StartedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	if t.Reason == "OOMKilled" {
		return Result{Elapsed: elapsed}, unknown("container step ran out of memory (%d MB)", s.MemoryMB)
	}
	if p.Status.Reason == "DeadlineExceeded" {
		return Result{Elapsed: elapsed}, unknown("container step timed out")
	}
	var log string
	limit := (s.OutputBytes+LogBytes)*4/3 + 8<<10
	q := url.Values{"container": {"main"}, "limitBytes": {strconv.Itoa(limit)}}
	if err := k.do(ctx, http.MethodGet, k.nsPath("pods")+"/"+name+"/log?"+q.Encode(), nil, &log); err != nil {
		return Result{Elapsed: elapsed}, unknown("reading the container's result: %v", err)
	}
	o, ok := ParseOutcome(log)
	if !ok {
		return Result{Elapsed: elapsed}, unknown("container ended without a result (exit %d, %s)", t.ExitCode, t.Reason)
	}
	return Settle(o, elapsed)
}

// remove deletes an attempt's Pod and Secret, whatever the step's context.
func (k *Kubernetes) remove(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "gracePeriodSeconds": 0, "propagationPolicy": "Background"}
	for _, kind := range []string{"pods", "secrets"} {
		err := k.do(ctx, http.MethodDelete, k.nsPath(kind)+"/"+name, opts, nil)
		var ae *apiError
		if err != nil && (!errors.As(err, &ae) || ae.Code != http.StatusNotFound) {
			k.logger().Warn("container runner: delete", "kind", kind, "name", name, "err", err)
		}
	}
}

// Sweep deletes sandbox Pods a dead worker left behind: finished ones
// older than ten minutes, and any older than the longest a step can take.
func (k *Kubernetes) Sweep(ctx context.Context) error {
	var list struct {
		Items []podStatus `json:"items"`
	}
	if err := k.do(ctx, http.MethodGet, k.nsPath("pods")+"?"+url.Values{"labelSelector": {SandboxLabel + "=true"}}.Encode(), nil, &list); err != nil {
		return err
	}
	for _, p := range list.Items {
		age := time.Since(p.Metadata.CreationTimestamp)
		done := p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed"
		if age > maxLifetime || (done && age > 10*time.Minute) {
			if strings.HasPrefix(p.Metadata.Name, "tsk-") {
				k.remove(p.Metadata.Name)
			}
		}
	}
	return nil
}
