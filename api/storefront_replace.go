package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// One workflow per storefront action (docs/spec/fronts.md F0): publishing
// a workflow whose storefront trigger names an action another workflow
// holds answers 409 action_taken, naming the action and the workflow that
// runs it, and the editor offers Replace it with this one: the same
// publish with replace, which takes the other workflow out of the
// environments this one deploys to and publishes this one, in one
// transaction, so the action never starts none and never two. Under
// four-eyes publishing it is refused with the reason, since the action
// would start nothing while the publish waited for a second person.

// actionTaken is a storefront action another workflow holds.
type actionTaken struct {
	*ingest.ActionTaken
	// Refused says why Replace is not offered ("" when it is).
	Refused string
}

func (e actionTaken) Error() string {
	return errConflict.Error() + ": " + strings.TrimPrefix(e.ActionTaken.Error(), ingest.ErrTriggerTaken.Error()+": ")
}

func (e actionTaken) Unwrap() error { return errConflict }

// actionWords are the storefront actions as the editor names them.
var actionWords = map[string]string{"order": "order form", "enquiry": "question form", "callback": "call-back form", "booking": "booking form"}

// writeActionTaken answers a publish that a held action refused.
func writeActionTaken(w http.ResponseWriter, e actionTaken) {
	words := actionWords[e.Action]
	if words == "" {
		words = e.Action + " form"
	}
	msg := fmt.Sprintf("Your storefront's %s already runs %q in %s. A form starts one workflow.", words, e.Name, e.Environment)
	if e.Refused == "" {
		msg += " Replace it with this one to take that one out of use and publish this one in its place."
	} else {
		msg += " " + e.Refused
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": msg, "code": "action_taken", "action": e.Action, "action_words": words, "environment": e.Environment,
		"workflow": map[string]any{"id": e.Workflow, "name": e.Name}, "can_replace": e.Refused == "", "refused": e.Refused,
	})
}

// storefrontActionOf is the storefront action version v of wf starts on
// ("" when it starts some other way).
func storefrontActionOf(ctx context.Context, tx pgx.Tx, wf uuid.UUID, v int) (string, error) {
	var def []byte
	if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&def); err != nil {
		return "", err
	}
	d, err := wd.Load(def)
	if err != nil || d.Trigger.Type != "storefront" {
		return "", nil //nolint:nilerr // an invalid definition is refused by the publishing checks
	}
	action, _ := d.Trigger.Config["action"].(string)
	return action, nil
}

// actionHolders are the workflows other than wf that hold version v's
// storefront action in the environments a publish deploys to.
func actionHolders(ctx context.Context, tx pgx.Tx, wf uuid.UUID, v int) ([]*ingest.ActionTaken, error) {
	action, err := storefrontActionOf(ctx, tx, wf, v)
	if err != nil || action == "" {
		return nil, err
	}
	envs, err := deployTargets(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	var out []*ingest.ActionTaken
	for _, env := range envs {
		held, err := ingest.StorefrontActionHolder(ctx, tx, env, action, wf)
		if err != nil {
			return nil, err
		}
		if held != nil {
			out = append(out, held)
		}
	}
	return out, nil
}

// replaceActionHolders takes the workflows holding version v's storefront
// action out of the environments it is about to be deployed to, each
// audited as an undeploy replaced by wf; one Git manages is refused. Runs
// they already started carry on.
func (s *Server) replaceActionHolders(ctx context.Context, tx pgx.Tx, wf uuid.UUID, v int, audit func(action, target string, detail map[string]any) error) error {
	held, err := actionHolders(ctx, tx, wf, v)
	if err != nil {
		return err
	}
	for _, h := range held {
		// A workflow Git manages is changed in Git, never behind it.
		if err := s.refuseGitManaged(ctx, tx, h.Workflow); err != nil {
			return fmt.Errorf("%w (%q runs your %s)", err, h.Name, actionWords[h.Action])
		}
		was, err := deployedVersion(ctx, tx, h.Workflow, h.Environment)
		if err != nil {
			return err
		}
		if err := undeployTx(ctx, tx, h.Workflow, h.Environment); err != nil {
			return err
		}
		if err := audit("workflow.undeploy", fmt.Sprintf("%s/%d", h.Workflow, was), map[string]any{"environment": h.Environment, "via": "replace",
			"replaced_by": wf.String(), "storefront_action": h.Action}); err != nil {
			return err
		}
	}
	return nil
}

// fourEyesRefusal is why Replace is not offered under four-eyes
// publishing.
func fourEyesRefusal(name string) string {
	return fmt.Sprintf("With four-eyes publishing, a second person approves each publish, so the form would start nothing while this one waited: take %q out of use first, then ask to publish this one.", name)
}

// asActionTaken reads a publish's error as a held action, if it is one.
func asActionTaken(err error) (actionTaken, bool) {
	var at actionTaken
	if errors.As(err, &at) {
		return at, true
	}
	var it *ingest.ActionTaken
	if errors.As(err, &it) {
		return actionTaken{ActionTaken: it}, true
	}
	return actionTaken{}, false
}
