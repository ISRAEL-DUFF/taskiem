-- Reading photos with AI (I3): handwriting a phone or a browser cannot
-- read is sent to the AI model, only where an owner turned it on. A photo
-- cannot be redacted, so it is the business's choice; nothing of the
-- photo is kept.
-- +goose Up
ALTER TABLE tenants ADD COLUMN ai_photo_reading boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE tenants DROP COLUMN ai_photo_reading;
