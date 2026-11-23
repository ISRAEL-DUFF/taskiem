-- Device fields D2 (docs/spec/device-fields.md §16, as built D2).
--
-- deliveries.proof_signature: the customer's signature a form step gave a
-- delivery (riders@1 record_proof), its place in the business's private
-- storage (a form file of the recording run), for the Confirm card. The
-- signer's name is not copied here: it stays sealed in the run's answer.
-- +goose Up
ALTER TABLE deliveries ADD COLUMN proof_signature text CHECK (proof_signature IS NULL OR length(proof_signature) <= 500);

-- +goose Down
ALTER TABLE deliveries DROP COLUMN proof_signature;
