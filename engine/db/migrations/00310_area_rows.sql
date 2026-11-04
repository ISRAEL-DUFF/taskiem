-- Business areas (docs/spec/business-areas.md, phase 3): the jobs that
-- message owners on their own (the money summaries, the leak check, the
-- evening stock total) look at which businesses have the area on, outside
-- any one business's scope. taskiem_area_rows answers the rows there are
-- for an area: the tenant and whether it is on. A business with no row
-- follows the default in engine/areas, which the caller applies.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_area_rows(p_area text)
RETURNS TABLE (tenant_id uuid, enabled boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.tenant_id, b.enabled FROM business_areas b WHERE b.area = p_area
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_area_rows(text) OWNER TO taskiem_dispatch;
CREATE POLICY dispatch ON business_areas FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, area, enabled) ON business_areas TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_area_rows(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_area_rows(text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_area_rows(text);
DROP POLICY dispatch ON business_areas;
REVOKE SELECT (tenant_id, area, enabled) ON business_areas FROM taskiem_dispatch;
