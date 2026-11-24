-- A chat's model calls keep no words in ai_interactions (finding F50): the
-- message routed through the model (kind intent) or asked of the tables
-- (table_query), and the model's answer, named people and amounts that
-- pattern redaction leaves, and the table is insert-only, outside the
-- chat history's expiry and erasure. Calls are recorded now with no
-- messages and no answer (api/chat_tables_ask.go recordModelCall); the
-- rows already kept are blanked here, keeping their tokens and outcome for
-- the budget and billing. Run as the migration owner: the application
-- stays insert-only. Only rows still holding words are touched.

-- +goose Up
UPDATE ai_interactions
   SET messages = '[]'::jsonb,
       response = NULL,
       error = CASE WHEN outcome = 'error' THEN error END
 WHERE kind IN ('intent', 'table_query')
   AND build_id IS NULL AND repair_id IS NULL AND run_id IS NULL
   AND (messages <> '[]'::jsonb OR response IS NOT NULL OR (error IS NOT NULL AND outcome <> 'error'));

-- +goose Down
-- The words are gone: nothing to restore.
SELECT 1;
