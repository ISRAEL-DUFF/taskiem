-- Device fields D1, on review (docs/spec/device-fields.md §15): what a
-- person sends in WhatsApp or Telegram towards a form step is their own
-- draft, keyed by the request and the person, so two members of a role
-- asked the same question do not mix their answers, and one cannot send
-- another's. Drafts of requests no longer waiting go now; from here they
-- go with the request's answer, its withdrawal or its expiry.
-- +goose Up
DELETE FROM form_chat_answers c USING form_requests r WHERE r.id = c.request_id AND r.status <> 'pending';
ALTER TABLE form_chat_answers DROP CONSTRAINT form_chat_answers_pkey;
ALTER TABLE form_chat_answers ADD PRIMARY KEY (request_id, user_id);

-- +goose Down
-- One draft per request again: the latest.
DELETE FROM form_chat_answers a WHERE EXISTS (SELECT 1 FROM form_chat_answers b
  WHERE b.request_id = a.request_id AND (b.updated_at, b.user_id) > (a.updated_at, a.user_id));
ALTER TABLE form_chat_answers DROP CONSTRAINT form_chat_answers_pkey;
ALTER TABLE form_chat_answers ADD PRIMARY KEY (request_id);
