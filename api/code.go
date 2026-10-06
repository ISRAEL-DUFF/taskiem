package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/israel-duff/taskiem/engine/flowcode"
)

// generateCode prints a definition as workflow code (spec 10.2), for the
// editor's code view.
func (s *Server) generateCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	doc, err := canonical(req.Definition)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	code, err := flowcode.Generate(r.Context(), doc)
	if err != nil {
		s.codeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code})
}

// compileCode builds workflow code into a definition and checks it, as
// publishing would. The code runs in the sandbox and may import only the SDK.
func (s *Server) compileCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Source) > 1<<20 {
		s.fail(w, r, fmt.Errorf("%w: source is larger than 1 MiB", errBadRequest))
		return
	}
	doc, err := flowcode.CompileSource(r.Context(), req.Source)
	if err != nil {
		s.codeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"definition": json.RawMessage(doc), "problems": nonNil(s.checkFor(r, doc))})
}

func (s *Server) codeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, flowcode.ErrCompile) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	s.fail(w, r, err)
}
