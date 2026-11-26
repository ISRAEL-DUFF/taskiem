-- Link previews in every chat (docs/spec/chat-previews-and-editing.md §2).
--
-- * people_files and group_files keep, besides a message's photo, voice
--   note or file, the picture of its link's preview: kind 'link_image',
--   sealed as the file is. A message has at most one of each kind. The
--   preview itself (the page's title, words and site) is sealed with the
--   message's words; nothing about the link is kept unsealed.
-- * A group message's sender may add its link's picture, as its file,
--   while the message stands.
-- * Chats between a person and a business keep the preview on each side's
--   message (customer_messages.preview, sealed under the customer's
--   subject as the words are; person_messages.preview), and the person's
--   copy of its picture in person_files with kind 'link_image'. The
--   business's copy of the picture is in its own storage, as its files are.
-- * Business-to-business chats keep theirs in link_messages.preview, which
--   only gains fields (site, image), so nothing changes here for them.
-- +goose Up
ALTER TABLE people_files ADD COLUMN kind text NOT NULL DEFAULT 'file' CHECK (kind IN ('file', 'link_image'));
ALTER TABLE people_files DROP CONSTRAINT people_files_pkey;
ALTER TABLE people_files ADD PRIMARY KEY (person_id, message_id, kind);

ALTER TABLE group_files ADD COLUMN kind text NOT NULL DEFAULT 'file' CHECK (kind IN ('file', 'link_image'));
ALTER TABLE group_files DROP CONSTRAINT group_files_pkey;
ALTER TABLE group_files ADD PRIMARY KEY (group_id, message_id, kind);

-- The sender adds a file to a message with one, or a link's picture to
-- any of their messages, while in the group.
DROP POLICY sender_insert ON group_files;
CREATE POLICY sender_insert ON group_files FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_files.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL)
    AND EXISTS (SELECT 1 FROM group_messages x WHERE x.group_id = group_files.group_id AND x.id = group_files.message_id AND x.sender_id = taskiem_person_scope()
                  AND x.kind <> 'event' AND x.deleted_at IS NULL AND (x.attachment IS NOT NULL OR group_files.kind = 'link_image')));

ALTER TABLE person_files ADD COLUMN kind text NOT NULL DEFAULT 'file' CHECK (kind IN ('file', 'link_image'));
ALTER TABLE person_messages ADD COLUMN preview jsonb CHECK (preview IS NULL OR jsonb_typeof(preview) = 'object');
ALTER TABLE customer_messages ADD COLUMN preview jsonb CHECK (preview IS NULL OR jsonb_typeof(preview) = 'object');

-- +goose Down
ALTER TABLE customer_messages DROP COLUMN preview;
ALTER TABLE person_messages DROP COLUMN preview;
DELETE FROM person_files WHERE kind = 'link_image';
ALTER TABLE person_files DROP COLUMN kind;

DROP POLICY sender_insert ON group_files;
CREATE POLICY sender_insert ON group_files FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_files.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL)
    AND EXISTS (SELECT 1 FROM group_messages x WHERE x.group_id = group_files.group_id AND x.id = group_files.message_id AND x.sender_id = taskiem_person_scope()
                  AND x.kind <> 'event' AND x.deleted_at IS NULL AND x.attachment IS NOT NULL));
DELETE FROM group_files WHERE kind = 'link_image';
ALTER TABLE group_files DROP CONSTRAINT group_files_pkey;
ALTER TABLE group_files ADD PRIMARY KEY (group_id, message_id);
ALTER TABLE group_files DROP COLUMN kind;

DELETE FROM people_files WHERE kind = 'link_image';
ALTER TABLE people_files DROP CONSTRAINT people_files_pkey;
ALTER TABLE people_files ADD PRIMARY KEY (person_id, message_id);
ALTER TABLE people_files DROP COLUMN kind;
