package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moderation"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Trust and safety on publishing and on the catalogue (storefronts spec
// 7, decision 0036, 13; docs/moderation.md). Every way of publishing (the
// web app, chat, an agent, a settings change that republishes) goes
// through publishGated:
//
//   - a page needs a verified phone (7.1);
//   - its plan's item limit, and payment when ordering only with a
//     verified business on a plan that has it;
//   - the word list, prices and the model: a block refuses the change;
//   - a first publish waits for a person when the business is not
//     verified (an agent's visit, or the business checked) or anything is
//     flagged; a later change flagged stays up and waits for a look.

var errNeedsVerification = errors.New("verification needed")

// moderator is the checker, with the AI layer's model when there is one.
func (s *Server) moderator() *moderation.Checker {
	c := &moderation.Checker{}
	switch {
	case s.AI != nil && s.AI.Moderation != nil:
		c.Model = s.AI.Moderation
	case s.AI != nil && s.AI.Provider != nil:
		c.Model = s.AI.Provider
	}
	return c
}

// txCache keeps verdicts in moderation_checks.
type txCache struct {
	tx     pgx.Tx
	tenant uuid.UUID
}

func (c txCache) Get(ctx context.Context, hash []byte) (moderation.Finding, bool, error) {
	var f moderation.Finding
	err := c.tx.QueryRow(ctx, `SELECT verdict, category, reason, checker FROM moderation_checks WHERE tenant_id = $1 AND hash = $2`, c.tenant, hash).
		Scan(&f.Verdict, &f.Category, &f.Reason, &f.Checker)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, false, nil
	}
	return f, err == nil, err
}

func (c txCache) Put(ctx context.Context, hash []byte, f moderation.Finding) error {
	_, err := c.tx.Exec(ctx, `INSERT INTO moderation_checks (tenant_id, hash, verdict, category, reason, checker) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, hash) DO UPDATE SET verdict = EXCLUDED.verdict, category = EXCLUDED.category, reason = EXCLUDED.reason,
		checker = EXCLUDED.checker, created_at = now()`, c.tenant, hash, f.Verdict, f.Category, f.Reason, f.Checker)
	return err
}

// verification is what a business has proved.
type verification struct {
	Phone    bool `json:"phone"`
	Business bool `json:"business"` // checked, or onboarded by an agent
	Agent    bool `json:"agent_onboarded"`
}

func verificationOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (verification, error) {
	var v verification
	err := tx.QueryRow(ctx, `SELECT taskiem_tenant_phone_verified($1),
		EXISTS (SELECT 1 FROM tenant_verifications WHERE tenant_id = $1 AND level IN ('business', 'agent_onboarded')),
		EXISTS (SELECT 1 FROM tenant_verifications WHERE tenant_id = $1 AND level = 'agent_onboarded')`, tenant).Scan(&v.Phone, &v.Business, &v.Agent)
	return v, err
}

// pageTexts are a page's words to check: the card, then the items.
func pageTexts(card storefront.Card, items []storefront.Item) []moderation.Text {
	out := []moderation.Text{{Field: "name", Text: card.Name}, {Field: "tagline", Text: card.Tagline}, {Field: "about", Text: card.About},
		{Field: "address", Text: card.Address}}
	for _, it := range items {
		out = append(out, itemTexts(it)...)
	}
	return out
}

func itemTexts(it storefront.Item) []moderation.Text {
	return []moderation.Text{{Field: "item " + it.ID, Text: it.Name}, {Field: "item " + it.ID, Text: it.Description}, {Field: "item " + it.ID, Text: it.Category}}
}

// checkPage runs the checks over a page's words and its items' prices.
func (s *Server) checkPage(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, card storefront.Card, items []storefront.Item) (moderation.Result, error) {
	m := s.moderator()
	res, err := m.Texts(ctx, txCache{tx, tenant}, pageTexts(card, items))
	if err != nil {
		return res, err
	}
	for _, it := range items {
		if it.DurationMinutes == 0 || it.PriceKobo != nil {
			f := m.Price("item "+it.ID, it.PriceKobo)
			f.Text = it.Name + " " + f.Text
			res.Add(f)
		}
	}
	return res, nil
}

// blockedErr says what a block refused, for the owner.
func blockedErr(res moderation.Result) error {
	var why []string
	for _, f := range res.Findings {
		if f.Verdict == moderation.Blocked {
			why = append(why, fmt.Sprintf("%q (%s)", truncateRunes(f.Text, 60), f.Reason))
		}
	}
	return fmt.Errorf("%w: this cannot go on a public page: %s. See what Taskiem does not allow: docs/moderation.md", storefront.ErrInvalid,
		strings.Join(why, "; "))
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// publishResult is a publish that went through the checks.
type publishResult struct {
	Storefront storefront.Storefront
	// Held: the version waits for a person before going live.
	Held   bool
	Checks moderation.Result
}

// publishGated publishes the tenant's draft in tx after the checks. The
// catalogue copy is read as it is: callers refresh it first.
func (s *Server) publishGated(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, actor string) (publishResult, error) {
	sf, err := storefront.Get(ctx, tx, tenant)
	if err != nil {
		return publishResult{}, err
	}
	if sf.Status == "suspended" {
		return publishResult{}, storefront.ErrSuspended
	}
	v, err := verificationOf(ctx, tx, tenant)
	if err != nil {
		return publishResult{}, err
	}
	if !v.Phone {
		return publishResult{}, fmt.Errorf("%w: publishing needs a verified phone: link your WhatsApp or Telegram (Account), or have a field agent set the page up", errNeedsVerification)
	}
	items, err := storefront.Catalogue(ctx, tx, tenant)
	if err != nil {
		return publishResult{}, err
	}
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return publishResult{}, err
	}
	if lim.StorefrontItems > 0 && len(items) > lim.StorefrontItems {
		return publishResult{}, fmt.Errorf("%w: your plan shows up to %d products and services, and the page has %d: hide some, or change plan",
			storefront.ErrInvalid, lim.StorefrontItems, len(items))
	}
	if payOnOrder, err := storefrontPaysOnOrder(ctx, tx, tenant); err != nil {
		return publishResult{}, err
	} else if payOnOrder {
		if err := s.payOnOrderAllowed(ctx, tenant, v); err != nil {
			return publishResult{}, err
		}
	}
	res, err := s.checkPage(ctx, tx, tenant, sf.Draft, items)
	if err != nil {
		return publishResult{}, err
	}
	if res.Verdict == moderation.Blocked {
		return publishResult{Checks: res}, blockedErr(res)
	}
	if err := s.notPlatformHost(sf.Slug); err != nil {
		return publishResult{}, err
	}
	first := sf.PublishedVersion == 0 || sf.Status == "in_review"
	hold := first && (!v.Business || res.Verdict == moderation.Flagged)
	if sf, err = storefront.Publish(ctx, tx, tenant, s.previewDomain(), actor, hold); err != nil {
		return publishResult{}, err
	}
	switch {
	case hold:
		err = queueReview(ctx, tx, tenant, sf, "first_publish", res.Findings, map[string]any{"verified": v.Business, "model_skipped": res.ModelSkipped}, actor)
	case res.Verdict == moderation.Flagged:
		err = queueReview(ctx, tx, tenant, sf, "edit", res.Findings, nil, actor)
	}
	return publishResult{Storefront: sf, Held: hold, Checks: res}, err
}

// publishRefusal is why a publish was refused, in words for the owner,
// when it was refused for something they can act on.
func publishRefusal(err error) (string, bool) {
	var fe *billing.FeatureError
	switch {
	case err == nil:
		return "", false
	case errors.As(err, &fe):
		return "your plan does not include this; change plan in Taskiem (Billing)", true
	case errors.Is(err, storefront.ErrInvalid), errors.Is(err, storefront.ErrSlugTaken), errors.Is(err, storefront.ErrSuspended),
		errors.Is(err, errNeedsVerification):
		return plainError(err), true
	}
	return "", false
}

// payOnOrderAllowed: payment when ordering needs a verified business and
// a plan that has it (storefronts 7.1, 11).
func (s *Server) payOnOrderAllowed(ctx context.Context, tenant uuid.UUID, v verification) error {
	if !v.Business {
		return fmt.Errorf("%w: taking payment when customers order needs a verified business: ask for payment after you accept instead, or ask Taskiem to verify the business", errNeedsVerification)
	}
	return s.Billing.Require(ctx, tenant, billing.FeatureStorefrontPayOnOrder)
}

func storefrontPaysOnOrder(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (bool, error) {
	acts, err := storefront.Actions(ctx, tx, tenant)
	if err != nil {
		return false, err
	}
	for _, a := range acts {
		if a.Enabled && a.Payment == "at_order" {
			return true, nil
		}
	}
	return false, nil
}

// queueReview adds to the review queue, or refreshes the page's pending
// review of that kind.
func queueReview(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, sf storefront.Storefront, kind string, findings []moderation.Finding,
	detail map[string]any, actor string) error {
	if findings == nil {
		findings = []moderation.Finding{}
	}
	if detail == nil {
		detail = map[string]any{}
	}
	f, _ := json.Marshal(findings)
	d, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `INSERT INTO storefront_reviews (id, tenant_id, storefront_id, kind, version, findings, detail, created_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, 0), $6, $7, $8)
		ON CONFLICT (storefront_id, kind) WHERE status = 'pending'
		DO UPDATE SET version = EXCLUDED.version, findings = EXCLUDED.findings, detail = EXCLUDED.detail, created_by = EXCLUDED.created_by`,
		uuid.Must(uuid.NewV7()), tenant, sf.ID, kind, sf.PublishedVersion, f, d, actor)
	return err
}

// moderationWait bounds the model's part of one list's checks: what it
// has not judged by then is kept (the words and prices still apply) and
// asked again at the next refresh, the verdicts it gave being kept.
const moderationWait = 2 * time.Minute

// memCache is the verdicts kept for a set of texts, read in a short
// transaction before the model is asked, and those the model gave, kept
// in the transaction that writes after: no transaction stays open while
// the model answers (finding F16).
type memCache struct {
	have, put map[string]moderation.Finding
}

func (c *memCache) Get(_ context.Context, hash []byte) (moderation.Finding, bool, error) {
	if f, ok := c.put[string(hash)]; ok {
		return f, true, nil
	}
	f, ok := c.have[string(hash)]
	return f, ok, nil
}

func (c *memCache) Put(_ context.Context, hash []byte, f moderation.Finding) error {
	c.put[string(hash)] = f
	return nil
}

// save keeps the model's new verdicts in moderation_checks.
func (c *memCache) save(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	for h, f := range c.put {
		if err := (txCache{tx, tenant}).Put(ctx, []byte(h), f); err != nil {
			return err
		}
	}
	return nil
}

// readVerdicts reads the verdicts kept for texts, as Checker.Texts looks
// them up.
func readVerdicts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, texts []moderation.Text) (*memCache, error) {
	c := &memCache{have: map[string]moderation.Finding{}, put: map[string]moderation.Finding{}}
	var hashes [][]byte
	for _, t := range texts {
		if t := strings.TrimSpace(t.Text); t != "" {
			hashes = append(hashes, moderation.Hash(t))
		}
	}
	if len(hashes) == 0 {
		return c, nil
	}
	rows, err := tx.Query(ctx, `SELECT hash, verdict, category, reason, checker FROM moderation_checks WHERE tenant_id = $1 AND hash = ANY($2)`, tenant, hashes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h []byte
		var f moderation.Finding
		if err := rows.Scan(&h, &f.Verdict, &f.Category, &f.Reason, &f.Checker); err != nil {
			return nil, err
		}
		c.have[string(h)] = f
	}
	return c, rows.Err()
}

// itemChecks is a list's items after the checks, and what the transaction
// that copies them records.
type itemChecks struct {
	items   []storefront.Item
	flagged []moderation.Finding
	cache   *memCache
}

// moderateItems checks a live page's items before they are copied:
// blocked items are left off the page, flagged ones stay and wait for a
// look, and items beyond the plan's limit are left off. It holds no
// transaction while the model answers: the kept verdicts are read first,
// every item's new pieces go to the model together (in requests of
// moderation.MaxPiecesPerCall), and record keeps the new verdicts and the
// review in the copy's transaction (finding F16).
func (s *Server) moderateItems(ctx context.Context, tenant uuid.UUID, items []storefront.Item) (itemChecks, error) {
	out := itemChecks{items: items}
	var live bool
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sf, err := storefront.Get(ctx, tx, tenant)
		live = err == nil && sf.PublishedVersion > 0
		return err
	}); err != nil || !live {
		return out, err
	}
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return out, err
	}
	if lim.StorefrontItems > 0 && len(items) > lim.StorefrontItems {
		items = items[:lim.StorefrontItems]
	}
	var texts []moderation.Text
	for _, it := range items {
		texts = append(texts, itemTexts(it)...)
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		out.cache, err = readVerdicts(ctx, tx, tenant, texts)
		return err
	}); err != nil {
		return out, err
	}
	m := s.moderator()
	mctx, cancel := context.WithTimeout(ctx, moderationWait)
	all, err := m.Texts(mctx, out.cache, texts)
	cancel()
	if err != nil {
		return out, err
	}
	byItem := map[string][]moderation.Finding{}
	for _, f := range all.Findings {
		byItem[f.Field] = append(byItem[f.Field], f)
	}
	out.items = items[:0:0]
	for _, it := range items {
		res := moderation.Result{Verdict: moderation.Allowed}
		for _, f := range byItem["item "+it.ID] {
			res.Add(f)
		}
		if it.DurationMinutes == 0 || it.PriceKobo != nil {
			f := m.Price("item "+it.ID, it.PriceKobo)
			f.Text = it.Name + " " + f.Text
			res.Add(f)
		}
		switch res.Verdict {
		case moderation.Blocked:
			out.flagged = append(out.flagged, res.Findings...)
			continue
		case moderation.Flagged:
			out.flagged = append(out.flagged, res.Findings...)
		}
		out.items = append(out.items, it)
	}
	return out, nil
}

// record keeps the model's new verdicts and queues what was flagged, in
// the transaction that copies the items.
func (c itemChecks) record(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	if c.cache != nil {
		if err := c.cache.save(ctx, tx, tenant); err != nil {
			return err
		}
	}
	if len(c.flagged) == 0 {
		return nil
	}
	sf, err := storefront.Get(ctx, tx, tenant)
	if err != nil {
		return err
	}
	return queueReview(ctx, tx, tenant, sf, "catalogue", c.flagged, nil, "system:moderation")
}

// prewarmPublish is prewarmPageChecks for a publish about to start (with
// liveOnly, one that happens only on a live page): a failure only means
// the publish's own checks ask the model.
func (s *Server) prewarmPublish(ctx context.Context, tenant uuid.UUID, liveOnly bool) {
	if err := s.prewarmPageChecks(ctx, tenant, liveOnly); err != nil && !errors.Is(err, storefront.ErrNotFound) && ctx.Err() == nil {
		s.Logger.Warn("storefront: checking the page before publishing", "tenant", tenant, "err", err)
	}
}

// prewarmPageChecks asks the model about the draft and the catalogue copy
// before a publish's transaction opens, and keeps its verdicts, so the
// checks inside that transaction read them rather than wait on the model
// (finding F16). What is not answered here is asked there, as before.
func (s *Server) prewarmPageChecks(ctx context.Context, tenant uuid.UUID, liveOnly bool) error {
	var texts []moderation.Text
	var cache *memCache
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sf, err := storefront.Get(ctx, tx, tenant)
		if err != nil || (liveOnly && sf.PublishedVersion == 0) {
			return err
		}
		items, err := storefront.Catalogue(ctx, tx, tenant)
		if err != nil {
			return err
		}
		texts = pageTexts(sf.Draft, items)
		cache, err = readVerdicts(ctx, tx, tenant, texts)
		return err
	}); err != nil {
		return err
	}
	m := s.moderator()
	if m.Model == nil || cache == nil {
		return nil
	}
	mctx, cancel := context.WithTimeout(ctx, moderationWait)
	_, err := m.Texts(mctx, cache, texts)
	cancel()
	if err != nil || len(cache.put) == 0 {
		return err
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return cache.save(ctx, tx, tenant)
	})
}
