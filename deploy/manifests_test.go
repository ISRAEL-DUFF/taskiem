// Package deploy holds no Go code: these tests read the deployment files.
// deploy/kubernetes/taskiem.yaml is the Helm chart rendered with its
// defaults (make chart; CI fails when it is stale), so checking it checks
// the chart.
package deploy

import (
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// object is a Kubernetes object, as far as these tests read it.
type object struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Type     string `json:"type"`
		Template struct {
			Spec podSpec `json:"spec"`
		} `json:"template"`
		Ports []struct {
			Name string `json:"name"`
		} `json:"ports"`
		Endpoints []struct {
			Port string `json:"port"`
		} `json:"endpoints"`
	} `json:"spec"`
}

type podSpec struct {
	Containers []struct {
		Name    string `json:"name"`
		EnvFrom []struct {
			SecretRef *struct {
				Name string `json:"name"`
			} `json:"secretRef"`
		} `json:"envFrom"`
		Env []struct {
			Name      string `json:"name"`
			ValueFrom *struct {
				SecretKeyRef *struct {
					Name string `json:"name"`
					Key  string `json:"key"`
				} `json:"secretKeyRef"`
			} `json:"valueFrom"`
		} `json:"env"`
	} `json:"containers"`
}

func manifests(t *testing.T) []object {
	t.Helper()
	b, err := os.ReadFile("kubernetes/taskiem.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var out []object
	for _, doc := range strings.Split(string(b), "\n---\n") {
		var o object
		if err := yaml.Unmarshal([]byte(doc), &o); err != nil {
			t.Fatalf("%v in:\n%s", err, doc)
		}
		if o.Kind != "" {
			out = append(out, o)
		}
	}
	if len(out) < 10 {
		t.Fatalf("only %d objects in kubernetes/taskiem.yaml", len(out))
	}
	return out
}

// TestPodsNeverGetTheOwnersLogin: only the migrate Job reads the
// migration Secret, whose login owns the schema and could RESET ROLE past
// row-level security; the pods' Secret holds a login of their own, which
// the Job signs up as a member of taskiem_app.
func TestPodsNeverGetTheOwnersLogin(t *testing.T) {
	const migrateSecret = "taskiem-migrate"
	jobs := 0
	for _, o := range manifests(t) {
		for _, c := range o.Spec.Template.Spec.Containers {
			for _, e := range c.EnvFrom {
				if e.SecretRef != nil && e.SecretRef.Name == migrateSecret {
					t.Errorf("%s %s: envFrom the migration Secret", o.Kind, o.Metadata.Name)
				}
			}
			env := map[string]string{}
			for _, e := range c.Env {
				if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
					env[e.Name] = e.ValueFrom.SecretKeyRef.Name + "/" + e.ValueFrom.SecretKeyRef.Key
				}
			}
			if o.Kind == "Job" && c.Name == "migrate" {
				jobs++
				if env["TASKIEM_DATABASE_URL"] != migrateSecret+"/TASKIEM_DATABASE_URL" || env["TASKIEM_APP_DATABASE_URL"] != "taskiem/TASKIEM_DATABASE_URL" {
					t.Errorf("migrate Job: owner from %q, app login from %q", env["TASKIEM_DATABASE_URL"], env["TASKIEM_APP_DATABASE_URL"])
				}
				continue
			}
			for name, ref := range env {
				if strings.HasPrefix(ref, migrateSecret+"/") {
					t.Errorf("%s %s: %s from the migration Secret", o.Kind, o.Metadata.Name, name)
				}
			}
		}
	}
	if jobs != 1 {
		t.Errorf("%d migrate Jobs", jobs)
	}
}
