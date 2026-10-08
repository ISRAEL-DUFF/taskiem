package wd

import (
	"strings"
	"testing"
	"time"
)

const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func containerDoc(cfg string) []byte {
	return doc(`{"id":"render","type":"container","input":{"html":"=trigger.body.html","key":"=secrets.api_key"},"config":` + cfg + `}`)
}

func TestContainerStepValid(t *testing.T) {
	for _, cfg := range []string{
		`{"image":"registry.example.com/tools/pdf@` + digest + `","command":["/bin/render"]}`,
		`{"image":"docker.io/library/python@` + digest + `","command":["python","/app/main.py"],"args":["--fast"],
		  "input_mode":"file","output_mode":"file","secrets":["api_key"],"secrets_mode":"file","class":"read",
		  "network":"egress","hosts":["api.example.com","*.example.org"],
		  "limits":{"cpu":"1.5","memory_mb":1024,"timeout":"10m","output_bytes":65536}}`,
		`{"image":"localhost:5000/x@` + digest + `","command":["x"],"class":"idempotent_write","limits":{"cpu":"250m"}}`,
	} {
		if p := Validate(containerDoc(cfg)); len(p) > 0 {
			t.Errorf("%s: unexpected problems: %v", cfg, p)
		}
	}
	d, err := Load(containerDoc(`{"image":"registry.example.com/tools/pdf@` + digest + `","command":["/bin/render"],"secrets":["api_key"]}`))
	if err != nil {
		t.Fatal(err)
	}
	s := d.Step("render")
	if s.Container == nil || s.Container.Command[0] != "/bin/render" || s.Queue() != "container" || s.Container.ContainerClass() != "unsafe_write" {
		t.Fatalf("parsed %+v (queue %s)", s.Container, s.Queue())
	}
}

func TestContainerStepInvalid(t *testing.T) {
	img := `"registry.example.com/tools/pdf@` + digest + `"`
	cases := map[string]struct{ cfg, want string }{
		"tag only":           {`{"image":"registry.example.com/tools/pdf:1.2","command":["x"]}`, "/config/image"},
		"tag and digest":     {`{"image":"registry.example.com/tools/pdf:1.2@` + digest + `","command":["x"]}`, "tags are refused"},
		"no registry":        {`{"image":"library/python@` + digest + `","command":["x"]}`, "must name its registry"},
		"bare name":          {`{"image":"python@` + digest + `","command":["x"]}`, "must name its registry"},
		"short digest":       {`{"image":"registry.example.com/x@sha256:abc","command":["x"]}`, "/config/image"},
		"no command":         {`{"image":` + img + `}`, "/config"},
		"empty command":      {`{"image":` + img + `,"command":[]}`, "/config/command"},
		"cpu above max":      {`{"image":` + img + `,"command":["x"],"limits":{"cpu":"8"}}`, "above the maximum"},
		"cpu below min":      {`{"image":` + img + `,"command":["x"],"limits":{"cpu":"10m"}}`, "below the minimum"},
		"memory above max":   {`{"image":` + img + `,"command":["x"],"limits":{"memory_mb":100000}}`, "memory_mb"},
		"timeout above max":  {`{"image":` + img + `,"command":["x"],"limits":{"timeout":"2h"}}`, "timeout 2h is above"},
		"output above max":   {`{"image":` + img + `,"command":["x"],"limits":{"output_bytes":99999999}}`, "output_bytes"},
		"egress needs hosts": {`{"image":` + img + `,"command":["x"],"network":"egress"}`, "/config"},
		"hosts need egress":  {`{"image":` + img + `,"command":["x"],"hosts":["a.example.com"]}`, "/config"},
		"reconcilable class": {`{"image":` + img + `,"command":["x"],"class":"reconcilable_write"}`, "/config/class"},
		"secret in args":     {`{"image":` + img + `,"command":["x"],"args":["=secrets.api_key"]}`, "secrets may only be used"},
		"unknown field":      {`{"image":` + img + `,"command":["x"],"privileged":true}`, "/config"},
		"bad secret name":    {`{"image":` + img + `,"command":["x"],"secrets":["a-b"]}`, "/config/secrets"},
	}
	for name, c := range cases {
		p := Validate(containerDoc(c.cfg))
		if len(p) == 0 {
			t.Errorf("%s: accepted", name)
			continue
		}
		var all []string
		for _, x := range p {
			all = append(all, x.String())
		}
		if !strings.Contains(strings.Join(all, "\n"), c.want) {
			t.Errorf("%s: problems %v do not mention %q", name, all, c.want)
		}
	}
}

func TestContainerLimitsClamped(t *testing.T) {
	c := &ContainerConfig{}
	if e := c.Effective(); e.CPUMillis != DefaultContainerCPUMillis || e.MemoryMB != DefaultContainerMemoryMB || e.Timeout != DefaultContainerTimeout || e.OutputBytes != DefaultContainerOutputBytes {
		t.Fatalf("defaults: %+v", e)
	}
	// A definition stored before validation refused these is clamped.
	c.Limits = &ContainerLimits{CPU: "64", MemoryMB: 1 << 20, Timeout: "48h", OutputBytes: 1 << 30}
	e := c.Effective()
	if e.CPUMillis != MaxContainerCPUMillis || e.MemoryMB != MaxContainerMemoryMB || e.Timeout != MaxContainerTimeout || e.OutputBytes != MaxContainerOutputBytes {
		t.Fatalf("clamped: %+v", e)
	}
	c.Limits = &ContainerLimits{CPU: "1m", Timeout: "30s", MemoryMB: 64, OutputBytes: 10}
	e = c.Effective()
	if e.CPUMillis != MinContainerCPUMillis || e.Timeout != 30*time.Second || e.MemoryMB != 64 || e.OutputBytes != 10 {
		t.Fatalf("small: %+v", e)
	}
	for in, want := range map[string]int{"500m": 500, "1": 1000, "1.5": 1500, "0.25": 250} {
		if n, err := ParseCPU(in); err != nil || n != want {
			t.Errorf("ParseCPU(%q) = %d, %v", in, n, err)
		}
	}
}
