package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtext"
	"github.com/israel-duff/taskiem/templates"
)

// A tool Describe it drafts is given a free command, as a recipe installed
// from the library is (review M6, 2): the one the draft names, or the next
// free one when it is taken. A draft started from a recipe brings the
// recipe's companion (the daily report's or watch and act's schedule),
// saved beside it as its own draft and named in the read-back.

// freeDraftCommandTx gives the tool doc declares a free command, except
// against the workflow it is a version of (its own versions keep their
// word); nil when doc is no tool.
func (s *Server) freeDraftCommandTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, doc []byte, except uuid.UUID) ([]byte, *recipeInstall, error) {
	var d map[string]any
	if err := json.Unmarshal(doc, &d); err != nil {
		return doc, nil, nil //nolint:nilerr // not a document: the checks say why
	}
	tool, ok := d["tool"].(map[string]any)
	if !ok {
		return doc, nil, nil
	}
	asked, _ := tool["command"].(string)
	if !templates.ToolCommand.MatchString(asked) {
		return doc, nil, nil // the checks say why
	}
	given, err := s.freeToolCommandTx(ctx, tx, tenant, asked, except, nil)
	if err != nil {
		if strings.Contains(err.Error(), errCommandsTaken.Error()) {
			return doc, nil, nil // the publish refuses it, with the word
		}
		return nil, nil, err
	}
	rec := &recipeInstall{Asked: asked, Command: given, Changed: given != asked}
	if !rec.Changed {
		return doc, rec, nil
	}
	tool["command"] = given
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, nil, err
	}
	out, err := canonical(raw)
	return out, rec, err
}

// draftRecipe is the recipe a proposal started from, when it has a
// companion: the template and the values to fill the companion with (the
// proposal's own, the command given, and the examples for any the goal
// did not give).
func draftRecipe(prop []byte, command string) (*templates.Template, map[string]any) {
	var p struct {
		Template *builder.TemplateUse `json:"template"`
	}
	if json.Unmarshal(prop, &p) != nil || p.Template == nil {
		return nil, nil
	}
	tpl, ok := templates.Default().Get(p.Template.ID)
	if !ok || !tpl.IsRecipe() || tpl.Companion == nil {
		return nil, nil
	}
	vals := tpl.Examples()
	for k, v := range p.Template.Params {
		vals[k] = v
	}
	if command != "" {
		vals[tpl.CommandParam().Name] = command
	}
	return tpl, vals
}

// readBackOf is a proposal's read-back in a language: its steps, its tool
// with the command it would be given, and the companion it brings.
func (s *Server) readBackOf(ctx context.Context, tenant uuid.UUID, prop []byte, except uuid.UUID, t lang.Tag) []wdtext.Line {
	var p struct {
		Definition json.RawMessage `json:"definition"`
	}
	if json.Unmarshal(prop, &p) != nil || len(p.Definition) == 0 {
		return []wdtext.Line{}
	}
	doc := []byte(p.Definition)
	command := ""
	_ = db0(ctx, s, tenant, func(tx pgx.Tx) error {
		out, rec, err := s.freeDraftCommandTx(ctx, tx, tenant, doc, except)
		if err == nil && out != nil {
			doc = out
			if rec != nil {
				command = rec.Command
			}
		}
		return nil
	})
	lines, err := s.describeDoc(ctx, tenant, doc, t)
	if err != nil {
		return []wdtext.Line{}
	}
	if tpl, _ := draftRecipe(prop, command); tpl != nil {
		lines = append(lines, wdtext.Line{Number: "•", Text: lang.Default().Text(t, "wd.tool.companion", "name", tpl.Companion.Name)})
	}
	return lines
}

// describeDoc reads a definition aloud in a language.
func (s *Server) describeDoc(ctx context.Context, tenant uuid.UUID, doc []byte, t lang.Tag) ([]wdtext.Line, error) {
	def, err := wd.Load(doc)
	if err != nil {
		return nil, err
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err
	}
	return wdtext.DescribeIn(def, reg, t), nil
}
