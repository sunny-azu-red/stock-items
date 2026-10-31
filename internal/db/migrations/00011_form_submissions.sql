-- +goose Up
-- One-time form keys: every form that creates or changes something carries
-- a random key, and the transaction doing the work claims it here first.
-- Posting the same form again (a double click, a retry, Back and Save)
-- finds the key taken and saves nothing; result_id is the record the first
-- save made or changed, for sending the user there. Keys are kept for a
-- day (claims prune older ones by created_at, an instant).
CREATE TABLE form_submissions (
    key        text        PRIMARY KEY CHECK (key ~ '^[0-9a-f]{32}$'),
    result_id  bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX form_submissions_created_idx ON form_submissions (created_at);

-- +goose Down
-- Undoing a Bought frees the key of the form it came from, which its trash
-- snapshot keeps; the code before this migration doesn't know it.
UPDATE trash SET payload = payload - 'form_key' WHERE payload ? 'form_key';
DROP TABLE form_submissions;
