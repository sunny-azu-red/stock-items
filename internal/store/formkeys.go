package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// One-time form keys make a create or edit happen at most once. Every form
// that creates or changes something carries a random key, and the
// transaction doing the work claims it in form_submissions as its first
// statement, then records the record it saved under it. The same form
// posted again (a double click, a retry after "Still saving…", Back and
// Save) finds the key taken: nothing is done again, and the user goes to
// what the first save made. A claim of a key whose first save is still
// running waits for that transaction: once it commits, the key is taken;
// if it rolled back, the claim goes ahead and this save is the one. The
// key is checked before anything else, the version included, so a retry
// of an edit isn't refused as stale by its own first save.

// AlreadySavedError answers a form posted again whose first save went
// through: nothing was done this time. ID is the record that save made or
// changed (0 for none); Gone, when set, says that record was deleted since.
type AlreadySavedError struct {
	ID   int64
	Gone *StaleError
}

func (e *AlreadySavedError) Error() string { return "this form was saved already" }

// inFormTx runs fn in a transaction that first claims key (claimFormKey)
// and, when fn succeeds, records *id under it: the record fn made or
// changed, of kind what (WhatItem, …; "" when there's no record page to
// check). id may be nil, and a create sets it inside fn.
func (s *Store) inFormTx(ctx context.Context, key, what string, id *int64, fn func(pgx.Tx) error) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := claimFormKey(ctx, tx, key, what); err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			return err
		}
		if key == "" || id == nil {
			return nil
		}
		_, err := tx.Exec(ctx, `UPDATE form_submissions SET result_id = $2 WHERE key = $1`, key, *id)
		return err
	})
}

// claimFormKey takes a form's key for this transaction, or fails with an
// *AlreadySavedError when a save with it committed. It must be the
// transaction's first statement: it then holds only the new key row,
// which nothing else locks, so it can't upset the lock order, and nothing
// (no version check, no stock change) runs before a repeat is caught.
// An empty key claims nothing: direct store calls (tests) have none, and
// the web refuses forms without one.
func claimFormKey(ctx context.Context, tx pgx.Tx, key, what string) error {
	if key == "" {
		return nil
	}
	for range 2 {
		// ON CONFLICT waits for a transaction that inserted the same key and
		// is still running: when it commits this inserts nothing, when it
		// rolls back this inserts the key.
		tag, err := tx.Exec(ctx, `INSERT INTO form_submissions (key) VALUES ($1) ON CONFLICT (key) DO NOTHING`, key)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			err := savedWith(ctx, tx, key, what)
			if errors.Is(err, errNotSaved) {
				continue // pruned meanwhile: the key is free again
			}
			return err
		}
		// Keys are kept for a day: long enough for any retry. SKIP LOCKED, so
		// claims never wait for each other's pruning.
		_, err = tx.Exec(ctx, `
			DELETE FROM form_submissions WHERE key IN (
			  SELECT key FROM form_submissions WHERE created_at < now() - interval '1 day' LIMIT 100 FOR UPDATE SKIP LOCKED)`)
		return err
	}
	return errNotSaved
}

// errNotSaved means no committed save holds a key.
var errNotSaved = errors.New("form key not saved")

// savedWith returns the *AlreadySavedError for a key a save committed with,
// or errNotSaved. For a record of kind what, it says whether the record
// was deleted since.
func savedWith(ctx context.Context, q queryRower, key, what string) error {
	var id *int64
	err := q.QueryRow(ctx, `SELECT result_id FROM form_submissions WHERE key = $1`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotSaved
	}
	if err != nil {
		return err
	}
	saved := &AlreadySavedError{}
	if id != nil {
		saved.ID = *id
	}
	table, ok := versionedTables[what]
	if !ok || saved.ID == 0 {
		return saved
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, saved.ID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		err := goneError(ctx, q, what, saved.ID)
		if !errors.As(err, &saved.Gone) {
			return err
		}
	}
	return saved
}

// FormSaved reports whether a form with key was saved already: an
// *AlreadySavedError (see savedWith), or nil. It doesn't wait for a save
// still running; the claim inside the save does. Handlers check it first,
// so a repeat is answered before anything the first save changed (a
// record ticked off, a version moved on) can refuse it.
func (s *Store) FormSaved(ctx context.Context, key, what string) error {
	if key == "" {
		return nil
	}
	err := savedWith(ctx, s.pool, key, what)
	if errors.Is(err, errNotSaved) {
		return nil
	}
	return err
}

// freeFormKey forgets a key, so its form can be saved again: undoing a
// Bought takes back what its form saved.
func freeFormKey(ctx context.Context, tx pgx.Tx, key string) error {
	if key == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `DELETE FROM form_submissions WHERE key = $1`, key)
	return err
}
