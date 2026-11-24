-- A build goal typed in chat (WhatsApp, Telegram, Slack) was kept in plain
-- text, redacted by patterns only, in ai_builds.goal and in every model
-- call's prompt in ai_interactions (F50b): names, places and amounts the
-- patterns leave, in an insert-only table outside retention and erasure,
-- while the same words in the chat history are sealed under the member.
-- - ai_builds.goal_sealed: the goal of a build from chat, sealed under its
--   member as the chat history is (api: sealMemberText); goal stays '' for
--   such builds.
-- - Their model calls now keep tokens and what happened only, as a chat's
--   own model calls do (00393); the build keeps its proposal.
-- - Rows already kept for chat builds (not template picks, whose goal is
--   the template's id) lose their words here, keeping tokens and outcome
--   for the budget and billing. Run as the migration owner: the
--   application stays insert-only on ai_interactions.
-- +goose Up
ALTER TABLE ai_builds ADD COLUMN goal_sealed jsonb;

UPDATE ai_interactions i
   SET messages = '[]'::jsonb,
       response = NULL,
       error = CASE WHEN i.outcome = 'error' THEN i.error END
  FROM ai_builds b
 WHERE i.build_id = b.id AND b.channel <> 'web' AND b.template_id IS NULL
   AND (i.messages <> '[]'::jsonb OR i.response IS NOT NULL OR (i.error IS NOT NULL AND i.outcome <> 'error'));

UPDATE ai_builds SET goal = ''
 WHERE channel <> 'web' AND template_id IS NULL AND goal <> '' AND goal_sealed IS NULL;

-- +goose Down
-- The words are gone: nothing to restore.
ALTER TABLE ai_builds DROP COLUMN goal_sealed;
