package api

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/wd"
)

// storefrontAnswersDeployable refuses deploying a workflow that reads
// trigger.answers fields its storefront action's form does not ask
// (docs/spec/fronts.md §5.3): the workflow's side of the check the
// storefront's publish makes the other way round.
func storefrontAnswersDeployable(ctx context.Context, tx pgx.Tx, d *wd.Definition) error {
	if d.Trigger.Type != "storefront" {
		return nil
	}
	read := wd.AnswerFields(d)
	if len(read) == 0 {
		return nil
	}
	id, kind, err := ingest.StorefrontAction(ctx, tx, d.Trigger.Config)
	if errors.Is(err, ingest.ErrNoStorefrontAction) {
		return nil // refused, in its own words, when the trigger registers
	}
	if err != nil {
		return err
	}
	asked, err := storefront.AnswersAsked(ctx, tx, id)
	if err != nil {
		return err
	}
	var missing []string
	for _, q := range read {
		if !slices.Contains(asked, q) {
			missing = append(missing, "trigger.answers."+q)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	words := actionWords[kind]
	if words == "" {
		words = kind + " form"
	}
	return fmt.Errorf("%w: this workflow reads %s, which your storefront's %s does not ask: add the question to the form, or stop reading it", errConflict,
		strings.Join(missing, ", "), words)
}
