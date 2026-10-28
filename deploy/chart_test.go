package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"text/template"

	"sigs.k8s.io/yaml"
)

// helpers parses the chart's _helpers.tpl with stand-ins for the few Helm
// functions it uses, so its checks can be run without Helm.
func helpers(t *testing.T) *template.Template {
	t.Helper()
	src, err := os.ReadFile("helm/taskiem/templates/_helpers.tpl")
	if err != nil {
		t.Fatal(err)
	}
	var tmpl *template.Template
	toInt := func(v any) int {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
		return 0
	}
	tmpl = template.New("helpers").Funcs(template.FuncMap{
		"fail": func(msg string) (string, error) { return "", errors.New(msg) },
		"int":  toInt,
		"add": func(vs ...any) int {
			n := 0
			for _, v := range vs {
				n += toInt(v)
			}
			return n
		},
		"default": func(def, v any) any {
			if v == nil || v == "" || v == false {
				return def
			}
			return v
		},
		"include": func(name string, data any) (string, error) {
			var b bytes.Buffer
			err := tmpl.ExecuteTemplate(&b, name, data)
			return b.String(), err
		},
		"trunc":      func(n int, s string) string { return s[:min(n, len(s))] },
		"trimSuffix": func(suf, s string) string { return strings.TrimSuffix(s, suf) },
		"contains":   func(sub, s string) bool { return strings.Contains(s, sub) },
		"replace":    func(old, new, s string) string { return strings.ReplaceAll(s, old, new) },
		"quote":      func(v any) string { return fmt.Sprintf("%q", fmt.Sprint(v)) },
	})
	if _, err := tmpl.Parse(string(src)); err != nil {
		t.Fatal(err)
	}
	return tmpl
}

// values is the chart's values.yaml with overrides applied (a path of
// keys, then the value).
func values(t *testing.T, overrides map[string]any) map[string]any {
	t.Helper()
	b, err := os.ReadFile("helm/taskiem/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	for path, val := range overrides {
		keys := strings.Split(path, ".")
		m := v
		for _, k := range keys[:len(keys)-1] {
			m = m[k].(map[string]any)
		}
		m[keys[len(keys)-1]] = val
	}
	return v
}

// TestChartRefusesASecondSchedulerOnLocalStorage: each scheduler keeps
// its archive and audit anchors on its own disk, and the default claim is
// ReadWriteOnce under a Recreate strategy, so more than one scheduler is
// refused unless the archive goes to S3 and the volume is off.
func TestChartRefusesASecondSchedulerOnLocalStorage(t *testing.T) {
	tmpl := helpers(t)
	render := func(overrides map[string]any) (string, error) {
		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, "taskiem.schedulerReplicas", map[string]any{"Values": values(t, overrides)})
		return b.String(), err
	}
	if out, err := render(nil); err != nil || out != "1" {
		t.Fatalf("default: %q %v", out, err)
	}
	for _, bad := range []map[string]any{
		{"roles.scheduler.replicas": 2.0},
		{"roles.scheduler.replicas": 2.0, "config.TASKIEM_ARCHIVE_S3_BUCKET": "taskiem-archive"},
		{"roles.scheduler.replicas": 2.0, "storage.enabled": false},
	} {
		if out, err := render(bad); err == nil || !strings.Contains(err.Error(), "roles.scheduler.replicas") {
			t.Errorf("%v: %q %v", bad, out, err)
		}
	}
	if out, err := render(map[string]any{"roles.scheduler.replicas": 2.0, "storage.enabled": false, "config.TASKIEM_ARCHIVE_S3_BUCKET": "taskiem-archive"}); err != nil || out != "2" {
		t.Errorf("S3 and no volume: %q %v", out, err)
	}
}
