package deploy

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"text/template"

	"sigs.k8s.io/yaml"
)

// policy is the part of a NetworkPolicy these tests read.
type policy struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		PolicyTypes []string `json:"policyTypes"`
		Ingress     []rule   `json:"ingress"`
		Egress      []rule   `json:"egress"`
	} `json:"spec"`
}

type rule struct {
	From  []peer `json:"from"`
	To    []peer `json:"to"`
	Ports []struct {
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	} `json:"ports"`
}

type peer struct {
	NamespaceSelector *struct {
		MatchLabels map[string]string `json:"matchLabels"`
	} `json:"namespaceSelector"`
	IPBlock *struct {
		CIDR   string   `json:"cidr"`
		Except []string `json:"except"`
	} `json:"ipBlock"`
}

// renderPolicies renders one of the chart's NetworkPolicy templates without
// Helm: the helpers with the stand-ins chart_test.go uses, and the few more
// functions these templates call.
func renderPolicies(t *testing.T, file string, overrides map[string]any) map[string]policy {
	t.Helper()
	tmpl := helpers(t)
	tmpl.Funcs(template.FuncMap{
		"list":      func(v ...any) []any { return v },
		"splitList": func(sep, s string) []string { return strings.Split(s, sep) },
		"nindent": func(n int, s string) string {
			pad := strings.Repeat(" ", n)
			return "\n" + pad + strings.ReplaceAll(s, "\n", "\n"+pad)
		},
	})
	src, err := os.ReadFile("helm/taskiem/templates/" + file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.New(file).Parse(string(src)); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Values":  values(t, overrides),
		"Chart":   map[string]any{"Name": "taskiem", "Version": "0.0.0", "AppVersion": "0.0.0"},
		"Release": map[string]any{"Name": "taskiem", "Service": "Helm"},
	}
	if err := tmpl.ExecuteTemplate(&b, file, data); err != nil {
		t.Fatal(err)
	}
	out := map[string]policy{}
	for _, doc := range strings.Split(b.String(), "\n---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var p policy
		if err := yaml.Unmarshal([]byte(doc), &p); err != nil {
			t.Fatalf("%v:\n%s", err, doc)
		}
		if p.Kind != "NetworkPolicy" {
			t.Fatalf("not a NetworkPolicy:\n%s", doc)
		}
		out[p.Metadata.Name] = p
	}
	return out
}

// TestChartLimitsWhatPublicAndMailPodsReach: the public host and the mail
// role take traffic from the internet. With network policies on, they may
// reach DNS and the database and nothing else: not the API pods, OpenBao
// or the cloud's metadata address.
func TestChartLimitsWhatPublicAndMailPodsReach(t *testing.T) {
	on := map[string]any{"networkPolicy.enabled": true, "public.enabled": true, "mail.enabled": true}
	got := renderPolicies(t, "networkpolicy-egress.yaml", on)
	for _, name := range []string{"taskiem-public-egress", "taskiem-mail-egress"} {
		p, ok := got[name]
		if !ok || len(p.Spec.PolicyTypes) != 1 || p.Spec.PolicyTypes[0] != "Egress" {
			t.Fatalf("%s: %+v (rendered %d policies)", name, p, len(got))
		}
		ports := map[int]bool{}
		for _, r := range p.Spec.Egress {
			if len(r.To) == 0 || len(r.Ports) == 0 {
				t.Errorf("%s: a rule open to every address or every port: %+v", name, r)
			}
			for _, port := range r.Ports {
				ports[port.Port] = true
			}
			for _, to := range r.To {
				if to.IPBlock != nil && to.IPBlock.CIDR == "0.0.0.0/0" && !strings.Contains(strings.Join(to.IPBlock.Except, " "), "169.254.0.0/16") {
					t.Errorf("%s: reaches the metadata address: %+v", name, to.IPBlock)
				}
			}
		}
		if len(ports) != 2 || !ports[53] || !ports[5432] {
			t.Errorf("%s: egress ports %v, want DNS and the database only", name, ports)
		}
	}
	// With the database's addresses given, its port goes nowhere else.
	on["networkPolicy.databaseCIDRs"] = []any{"10.20.0.5/32"}
	on["networkPolicy.databasePort"] = 6432.0
	for _, r := range renderPolicies(t, "networkpolicy-egress.yaml", on)["taskiem-public-egress"].Spec.Egress {
		if r.Ports[0].Port != 6432 {
			continue
		}
		if len(r.To) != 1 || r.To[0].IPBlock == nil || r.To[0].IPBlock.CIDR != "10.20.0.5/32" {
			t.Errorf("database rule with databaseCIDRs: %+v", r.To)
		}
	}
	// Off, or a role not deployed: nothing.
	if n := len(renderPolicies(t, "networkpolicy-egress.yaml", map[string]any{"public.enabled": true})); n != 0 {
		t.Errorf("%d egress policies with networkPolicy off", n)
	}
	if n := len(renderPolicies(t, "networkpolicy-egress.yaml", map[string]any{"networkPolicy.enabled": true})); n != 0 {
		t.Errorf("%d egress policies with neither role on", n)
	}
}

// TestChartNarrowsTheAPIToTheIngressController: with the ingress
// controller's namespace given, the API and edge ports take traffic only
// from it; without it they stay open, as before.
func TestChartNarrowsTheAPIToTheIngressController(t *testing.T) {
	open := func(p policy, port int) (found, anywhere bool, from string) {
		for _, r := range p.Spec.Ingress {
			for _, pp := range r.Ports {
				if pp.Port != port {
					continue
				}
				found = true
				if len(r.From) == 0 {
					anywhere = true
				} else if r.From[0].NamespaceSelector != nil {
					from = r.From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]
				}
			}
		}
		return
	}
	got := renderPolicies(t, "networkpolicy.yaml", map[string]any{"networkPolicy.enabled": true})
	if found, anywhere, _ := open(got["taskiem-api"], 8080); !found || !anywhere {
		t.Errorf("default: api 8080 found %v, open %v", found, anywhere)
	}
	got = renderPolicies(t, "networkpolicy.yaml", map[string]any{"networkPolicy.enabled": true, "networkPolicy.ingressNamespace": "ingress-nginx"})
	for name, port := range map[string]int{"taskiem-api": 8080, "taskiem-edge": 8081} {
		if found, anywhere, from := open(got[name], port); !found || anywhere || from != "ingress-nginx" {
			t.Errorf("%s port %d: found %v, open to anywhere %v, from %q", name, port, found, anywhere, from)
		}
	}
	// Metrics are untouched: the monitoring namespace only.
	if _, anywhere, from := open(got["taskiem-api"], 9090); anywhere || from != "monitoring" {
		t.Errorf("metrics: open %v, from %q", anywhere, from)
	}
}
