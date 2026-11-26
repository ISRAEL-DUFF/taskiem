-- File previews in every chat (docs/spec/chat-previews-and-editing.md §1).
--
-- * The picture of a PDF's first page is kept next to the file, under the
--   same rules: in people_files, group_files and person_files with kind
--   'preview', sealed as the file is where the file is sealed. A message
--   has at most one of each kind (person_files gains the index the others
--   have in their keys).
-- * A group's picture is drawn the first time a member asks for it when it
--   was not drawn as it was sent (an older PDF): a member who sees the
--   message may add it, once, while the message stands and carries a PDF.
-- * What is known about the file (pages, protected, preview, w, h, doc)
--   is kept in each copy's attachment, as its kind, type and size are, so
--   nothing new is needed in the columns. Business-to-business chats and
--   a business's copy of a customer chat keep the picture in the
--   business's own storage, its place in the attachment (never shown).
-- +goose Up
ALTER TABLE people_files DROP CONSTRAINT people_files_kind_check;
ALTER TABLE people_files ADD CONSTRAINT people_files_kind_check CHECK (kind IN ('file', 'link_image', 'preview'));

ALTER TABLE group_files DROP CONSTRAINT group_files_kind_check;
ALTER TABLE group_files ADD CONSTRAINT group_files_kind_check CHECK (kind IN ('file', 'link_image', 'preview'));

CREATE POLICY member_preview_insert ON group_files FOR INSERT TO taskiem_app
  WITH CHECK (group_files.kind = 'preview' AND taskiem_person_business() IS NULL
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_files.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL)
    AND EXISTS (SELECT 1 FROM group_messages x WHERE x.group_id = group_files.group_id AND x.id = group_files.message_id
                  AND x.deleted_at IS NULL AND x.attachment ->> 'type' = 'application/pdf'));

ALTER TABLE person_files DROP CONSTRAINT person_files_kind_check;
ALTER TABLE person_files ADD CONSTRAINT person_files_kind_check CHECK (kind IN ('file', 'link_image', 'preview'));
CREATE UNIQUE INDEX person_files_one_preview ON person_files (message_id) WHERE kind = 'preview';

-- +goose Down
DROP INDEX person_files_one_preview;
DELETE FROM person_files WHERE kind = 'preview';
ALTER TABLE person_files DROP CONSTRAINT person_files_kind_check;
ALTER TABLE person_files ADD CONSTRAINT person_files_kind_check CHECK (kind IN ('file', 'link_image'));

DROP POLICY member_preview_insert ON group_files;
DELETE FROM group_files WHERE kind = 'preview';
ALTER TABLE group_files DROP CONSTRAINT group_files_kind_check;
ALTER TABLE group_files ADD CONSTRAINT group_files_kind_check CHECK (kind IN ('file', 'link_image'));

DELETE FROM people_files WHERE kind = 'preview';
ALTER TABLE people_files DROP CONSTRAINT people_files_kind_check;
ALTER TABLE people_files ADD CONSTRAINT people_files_kind_check CHECK (kind IN ('file', 'link_image'));
