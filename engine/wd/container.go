package wd

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ContainerConfig is a container step's config (spec 7.5,
// docs/container-steps.md): a pinned image run once per attempt in the
// container sandbox, with the step's input on stdin or in a file and its
// output as JSON on stdout or in a file.
type ContainerConfig struct {
	Image       string           `json:"image"`
	Command     []string         `json:"command"`
	Args        []string         `json:"args,omitempty"`
	InputMode   string           `json:"input_mode,omitempty"`
	OutputMode  string           `json:"output_mode,omitempty"`
	Secrets     []string         `json:"secrets,omitempty"`
	SecretsMode string           `json:"secrets_mode,omitempty"`
	Class       string           `json:"class,omitempty"`
	Network     string           `json:"network,omitempty"`
	Hosts       []string         `json:"hosts,omitempty"`
	Limits      *ContainerLimits `json:"limits,omitempty"`
}

// ContainerLimits are what a container step asks for; the platform clamps
// them to its maxima (and validation refuses more).
type ContainerLimits struct {
	CPU         string `json:"cpu,omitempty"`
	MemoryMB    int    `json:"memory_mb,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
	OutputBytes int    `json:"output_bytes,omitempty"`
}

// The platform's defaults and ceilings for a container step.
const (
	DefaultContainerCPUMillis   = 1000
	DefaultContainerMemoryMB    = 512
	DefaultContainerTimeout     = 5 * time.Minute
	DefaultContainerOutputBytes = 256 << 10

	MaxContainerCPUMillis   = 4000
	MaxContainerMemoryMB    = 4096
	MaxContainerTimeout     = 30 * time.Minute
	MaxContainerOutputBytes = 1 << 20
	MinContainerCPUMillis   = 100
)

// EffectiveContainerLimits are a step's limits with defaults filled in and
// everything clamped to the platform's ceilings.
type EffectiveContainerLimits struct {
	CPUMillis   int
	MemoryMB    int
	Timeout     time.Duration
	OutputBytes int
}

// ParseCPU parses a CPU quantity ("500m", "1", "1.5") into millicores.
func ParseCPU(s string) (int, error) {
	if m, ok := strings.CutSuffix(s, "m"); ok {
		n, err := strconv.Atoi(m)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid cpu %q", s)
		}
		return n, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1e6 {
		return 0, fmt.Errorf("invalid cpu %q", s)
	}
	return int(f*1000 + 0.5), nil
}

// Effective returns the limits a container step runs under: its own,
// defaulted and clamped. Definitions published before a ceiling was
// lowered are held to the new ceiling here.
func (c *ContainerConfig) Effective() EffectiveContainerLimits {
	e := EffectiveContainerLimits{CPUMillis: DefaultContainerCPUMillis, MemoryMB: DefaultContainerMemoryMB,
		Timeout: DefaultContainerTimeout, OutputBytes: DefaultContainerOutputBytes}
	if c == nil || c.Limits == nil {
		return e
	}
	l := c.Limits
	if n, err := ParseCPU(l.CPU); err == nil && n > 0 {
		e.CPUMillis = min(max(n, MinContainerCPUMillis), MaxContainerCPUMillis)
	}
	if l.MemoryMB > 0 {
		e.MemoryMB = min(l.MemoryMB, MaxContainerMemoryMB)
	}
	if d, err := ParseDuration(l.Timeout); err == nil && d > 0 {
		e.Timeout = min(d, MaxContainerTimeout)
	}
	if l.OutputBytes > 0 {
		e.OutputBytes = min(l.OutputBytes, MaxContainerOutputBytes)
	}
	return e
}

// ContainerClass is the step's effect class: unsafe_write unless declared.
func (c *ContainerConfig) ContainerClass() string {
	if c == nil || c.Class == "" {
		return "unsafe_write"
	}
	return c.Class
}

// SplitImage splits "registry/repo@sha256:..." into the repository (with
// its registry) and the digest.
func SplitImage(image string) (repo, digest string, ok bool) {
	repo, digest, ok = strings.Cut(image, "@")
	return repo, digest, ok && strings.HasPrefix(digest, "sha256:")
}

// ImageProblem explains why an image reference is refused, or "".
// References must name their registry and pin a digest; tags are refused,
// because a tag can be moved to other code after review.
func ImageProblem(image string) string {
	repo, _, ok := SplitImage(image)
	if !ok {
		return "image must be pinned by digest (…@sha256:<64 hex>)"
	}
	host, path, found := strings.Cut(repo, "/")
	if !found || path == "" {
		return "image must name its registry, e.g. registry.example.com/team/tool@sha256:…"
	}
	if !strings.ContainsAny(host, ".:") && host != "localhost" {
		return fmt.Sprintf("image must name its registry (%q is not a registry host; use e.g. docker.io/%s)", host, repo)
	}
	if strings.Contains(path, ":") {
		return "image tags are refused: pin the image by digest only (remove the :tag)"
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part != strings.ToLower(part) || strings.Trim(part, "abcdefghijklmnopqrstuvwxyz0123456789._-") != "" {
			return fmt.Sprintf("image repository %q is not a valid repository name", path)
		}
	}
	return ""
}

// containerConfig refuses what the schema cannot express: an image that is
// not pinned or names no registry, and limits above the ceilings.
func (v *validator) containerConfig(p string, cfg map[string]any) {
	if img, _ := cfg["image"].(string); img != "" {
		if msg := ImageProblem(img); msg != "" {
			v.add(p+"/image", "%s", msg)
		}
	}
	lim, _ := cfg["limits"].(map[string]any)
	lp := p + "/limits"
	if s, ok := lim["cpu"].(string); ok {
		if n, err := ParseCPU(s); err != nil {
			v.add(lp+"/cpu", "%v", err)
		} else if n > MaxContainerCPUMillis {
			v.add(lp+"/cpu", "cpu %s is above the maximum of %dm", s, MaxContainerCPUMillis)
		} else if n < MinContainerCPUMillis {
			v.add(lp+"/cpu", "cpu %s is below the minimum of %dm", s, MinContainerCPUMillis)
		}
	}
	if mb, ok := lim["memory_mb"].(float64); ok && mb > MaxContainerMemoryMB {
		v.add(lp+"/memory_mb", "memory_mb %v is above the maximum of %d", mb, MaxContainerMemoryMB)
	}
	if t, ok := lim["timeout"].(string); ok {
		if d, err := ParseDuration(t); err == nil && d > MaxContainerTimeout {
			v.add(lp+"/timeout", "timeout %s is above the maximum of %s", t, MaxContainerTimeout)
		}
	}
	if n, ok := lim["output_bytes"].(float64); ok && n > MaxContainerOutputBytes {
		v.add(lp+"/output_bytes", "output_bytes %v is above the maximum of %d", n, MaxContainerOutputBytes)
	}
}
