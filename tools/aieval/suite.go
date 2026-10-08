package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Expect is what a good answer to a request must have. Every field is a
// minimum, never an exact shape: a draft may use more connectors or step
// types than listed.
type Expect struct {
	// Connectors the workflow must use (refs, e.g. paystack@1), in a step
	// or as its trigger.
	Connectors []string `json:"connectors,omitempty"`
	// Steps are step types the workflow must use somewhere.
	Steps []string `json:"steps,omitempty"`
	// Trigger is the trigger type the workflow must start with.
	Trigger string `json:"trigger,omitempty"`
	// Properties are named rules the workflow must satisfy (rules.go).
	Properties []string `json:"properties,omitempty"`
}

// Case is one line of a builder suite.
type Case struct {
	ID         string   `json:"id"`
	Request    string   `json:"request"`
	Expect     Expect   `json:"expect"`
	Tags       []string `json:"tags,omitempty"`
	Difficulty string   `json:"difficulty,omitempty"` // easy | medium | hard
	// Source is where the request came from: synthetic (written by
	// engineering, pending review by people), dogfood, partner.
	Source string `json:"source,omitempty"`
}

var difficulties = []string{"easy", "medium", "hard"}

var stepTypes = []string{"connector", "http", "code", "transform", "branch", "parallel", "foreach", "wait", "signal", "approval"}

var triggerTypes = []string{"webhook", "schedule", "connector_event", "polling", "database_change", "manual", "whatsapp", "ussd", "email", "subflow"}

// check reports what is wrong with a case's own shape.
func (c *Case) check() error {
	if c.ID == "" || strings.TrimSpace(c.Request) == "" {
		return fmt.Errorf("every case needs an id and a request")
	}
	if len(c.Tags) == 0 {
		return fmt.Errorf("%s: at least one tag is required (the first is its group)", c.ID)
	}
	if !slices.Contains(difficulties, c.Difficulty) {
		return fmt.Errorf("%s: difficulty must be easy, medium or hard", c.ID)
	}
	for _, s := range c.Expect.Steps {
		if !slices.Contains(stepTypes, s) {
			return fmt.Errorf("%s: unknown step type %q", c.ID, s)
		}
	}
	if c.Expect.Trigger != "" && !slices.Contains(triggerTypes, c.Expect.Trigger) {
		return fmt.Errorf("%s: unknown trigger type %q", c.ID, c.Expect.Trigger)
	}
	for _, p := range c.Expect.Properties {
		if _, ok := rules[p]; !ok {
			return fmt.Errorf("%s: unknown property %q", c.ID, p)
		}
	}
	for _, r := range c.Expect.Connectors {
		if !strings.Contains(r, "@") {
			return fmt.Errorf("%s: connector %q must be a ref like paystack@1", c.ID, r)
		}
	}
	if len(c.Expect.Connectors)+len(c.Expect.Steps) == 0 {
		return fmt.Errorf("%s: expect at least one connector or step type", c.ID)
	}
	return nil
}

// suiteFiles lists the .jsonl files of path (a file or a directory).
func suiteFiles(path string) ([]string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return []string{path}, nil
	}
	files, err := filepath.Glob(filepath.Join(path, "*.jsonl"))
	sort.Strings(files)
	return files, err
}

// readJSONL decodes every non-comment line of the suite's files into fn.
func readJSONL(path string, fn func(file string, line int, raw []byte) error) error {
	files, err := suiteFiles(path)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s: no .jsonl files", path)
	}
	for _, f := range files {
		fh, err := os.Open(f) //nolint:gosec // the suite named on the command line
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		line := 0
		for sc.Scan() {
			line++
			text := strings.TrimSpace(sc.Text())
			if text == "" || strings.HasPrefix(text, "//") {
				continue
			}
			if err := fn(f, line, []byte(text)); err != nil {
				_ = fh.Close()
				return fmt.Errorf("%s:%d: %w", f, line, err)
			}
		}
		_ = fh.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	}
	return nil
}

func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// load reads a builder suite and checks every case.
func load(path string) ([]Case, error) {
	var out []Case
	seen := map[string]bool{}
	err := readJSONL(path, func(_ string, _ int, raw []byte) error {
		var c Case
		if err := decodeStrict(raw, &c); err != nil {
			return err
		}
		if c.Source == "" {
			c.Source = "synthetic"
		}
		if err := c.check(); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("duplicate id %q", c.ID)
		}
		seen[c.ID] = true
		out = append(out, c)
		return nil
	})
	return out, err
}

// filter keeps cases with one of tags (comma-separated) and at most
// limit of them.
func filter[T any](cases []T, tagsOf func(T) []string, tags string, limit int) []T {
	if tags != "" {
		want := map[string]bool{}
		for _, t := range strings.Split(tags, ",") {
			want[strings.TrimSpace(t)] = true
		}
		var kept []T
		for _, c := range cases {
			for _, t := range tagsOf(c) {
				if want[t] {
					kept = append(kept, c)
					break
				}
			}
		}
		cases = kept
	}
	if limit > 0 && len(cases) > limit {
		cases = cases[:limit]
	}
	return cases
}
