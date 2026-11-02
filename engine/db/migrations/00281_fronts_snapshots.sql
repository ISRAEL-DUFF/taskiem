-- Fronts F1d (docs/spec/fronts.md §5.1, §9): a published snapshot holds
-- its fronts. Every snapshot is rewritten to the new shape with the same
-- content: its home front (the storefront's first front, made by 00279)
-- with that front's blocks and the snapshot's actions, each given the id
-- and slug of the storefront's built-in action of its kind. The
-- snapshot's own top-level actions stay, for a public host on the old
-- binary, so the page renders the same on either. The hash, of the model,
-- is recomputed.

-- +goose Up
UPDATE storefront_snapshots s SET model = s.model || jsonb_build_object('fronts', jsonb_build_array(jsonb_strip_nulls(jsonb_build_object(
    'id', f.id, 'slug', f.slug, 'kind', f.kind, 'name', f.name, 'tagline', NULLIF(f.tagline, ''), 'photo', NULLIF(f.photo, ''),
    'blocks', (SELECT jsonb_agg(jsonb_build_object('kind', b.kind, 'config', b.config) ORDER BY b.position, b.id) FROM storefront_blocks b WHERE b.front_id = f.id),
    'actions', (SELECT jsonb_agg(CASE WHEN a.id IS NULL THEN x.v ELSE jsonb_build_object('id', a.id, 'slug', a.slug) || x.v END ORDER BY x.n)
                  FROM jsonb_array_elements(CASE WHEN jsonb_typeof(s.model->'actions') = 'array' THEN s.model->'actions' ELSE '[]' END) WITH ORDINALITY x(v, n)
                  LEFT JOIN storefront_actions a ON a.storefront_id = s.storefront_id AND a.kind = x.v->>'kind' AND a.builtin)))))
  FROM storefront_fronts f
 WHERE f.storefront_id = s.storefront_id AND f.slug = '' AND NOT s.model ? 'fronts';
UPDATE storefront_snapshots SET hash = sha256(convert_to(model::text, 'UTF8')) WHERE model ? 'fronts';

-- +goose Down
UPDATE storefront_snapshots SET model = model - 'fronts', hash = sha256(convert_to((model - 'fronts')::text, 'UTF8')) WHERE model ? 'fronts';
