package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// WhatSettings is the settings row, for StaleError and form keys. It has a
// version but no trash: settings are never deleted.
const WhatSettings = "settings"

// SettingsColumns are the settings table's own columns, in its order, as
// scanSettings reads them (TestSettingsComplete compares them with the
// table).
var SettingsColumns = []string{
	"currency", "distance_unit", "number_format", "date_format", "days_ago", "dashboard_recent",
	"activity_page_size", "services_page_size", "item_history",
	"sku_search_url", "sku_search_prefix", "expiring_days", "categories",
	"due_soon_days", "due_soon_percent", "due_soon_min_distance", "work_names",
	"warranty_soon_distance", "warranty_soon_days", "warranty_grace_percent", "warranty_grace_days",
}

const settingsSelect = `version, currency, distance_unit, number_format, date_format, days_ago, dashboard_recent,
	activity_page_size, services_page_size, item_history,
	sku_search_url, sku_search_prefix, expiring_days, categories,
	due_soon_days, due_soon_percent, due_soon_min_distance, work_names,
	warranty_soon_distance, warranty_soon_days, warranty_grace_percent, warranty_grace_days`

func scanSettings(row pgx.Row) (stock.Settings, error) {
	var s stock.Settings
	err := row.Scan(&s.Version, &s.Currency, &s.DistanceUnit, &s.NumberFormat, &s.DateFormat, &s.DaysAgo, &s.DashboardRecent,
		&s.ActivityPageSize, &s.ServicesPageSize, &s.ItemHistory,
		&s.SKUSearchURL, &s.SKUSearchPrefix, &s.ExpiringDays, &s.Categories,
		&s.DueSoonDays, &s.DueSoonPercent, &s.DueSoonMinDistance, &s.WorkNames,
		&s.WarrantySoonDistance, &s.WarrantySoonDays, &s.GracePercent, &s.GraceDays)
	return s, err
}

// GetSettings reads the settings row (made by migration 00010).
func (s *Store) GetSettings(ctx context.Context) (stock.Settings, error) {
	return scanSettings(s.pool.QueryRow(ctx, `SELECT `+settingsSelect+` FROM settings WHERE id = 1`))
}

// UpdateSettings saves the Settings page's form (validated by the caller)
// and returns what was saved. The form's key is claimed first; then the
// row is locked and its version checked (a *StaleError when another tab
// saved since). The settings row is locked by nothing else, so it needs no
// place in the lock order.
func (s *Store) UpdateSettings(ctx context.Context, in stock.SettingsInput) (stock.Settings, error) {
	var saved stock.Settings
	id := int64(1)
	err := s.inFormTx(ctx, in.FormKey, WhatSettings, &id, func(tx pgx.Tx) error {
		var err error
		saved, err = writeSettings(ctx, tx, in.Settings)
		return err
	})
	return saved, err
}

// writeSettings locks the row, checks set.Version against it and writes
// set.
func writeSettings(ctx context.Context, tx pgx.Tx, set stock.Settings) (stock.Settings, error) {
	var have int
	if err := tx.QueryRow(ctx, `SELECT version FROM settings WHERE id = 1 FOR UPDATE`).Scan(&have); err != nil {
		return stock.Settings{}, notFound(err)
	}
	if err := checkVersion(have, set.Version, WhatSettings); err != nil {
		return stock.Settings{}, err
	}
	// The page shows every setting: a save moves the version on.
	return scanSettings(tx.QueryRow(ctx, `
		UPDATE settings SET version = version + 1,
		       currency = $1, distance_unit = $2, number_format = $3, date_format = $4, days_ago = $5, dashboard_recent = $6,
		       activity_page_size = $7, services_page_size = $8, item_history = $9,
		       sku_search_url = $10, sku_search_prefix = $11, expiring_days = $12, categories = $13,
		       due_soon_days = $14, due_soon_percent = $15, due_soon_min_distance = $16, work_names = $17,
		       warranty_soon_distance = $18, warranty_soon_days = $19, warranty_grace_percent = $20, warranty_grace_days = $21,
		       updated_at = now()
		 WHERE id = 1
		 RETURNING `+settingsSelect,
		set.Currency, set.DistanceUnit, set.NumberFormat, set.DateFormat, set.DaysAgo, set.DashboardRecent,
		set.ActivityPageSize, set.ServicesPageSize, set.ItemHistory,
		set.SKUSearchURL, set.SKUSearchPrefix, set.ExpiringDays, nonNil(set.Categories),
		set.DueSoonDays, set.DueSoonPercent, set.DueSoonMinDistance, nonNil(set.WorkNames),
		set.WarrantySoonDistance, set.WarrantySoonDays, set.GracePercent, set.GraceDays))
}

// nonNil is an empty list for nil: the list columns are NOT NULL.
func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
