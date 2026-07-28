package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// GetDBTimestamp returns a UNIX timestamp from database time.
// Falls back to application time on error.
func GetDBTimestamp() int64 {
	return getDBTimestampTx(DB)
}

// getDBTimestampTx reads database time through the caller's transaction.
// Using the global pool from inside a transaction can deadlock deployments
// whose SQL pool has a single connection.
func getDBTimestampTx(tx *gorm.DB) int64 {
	if tx == nil {
		tx = DB
	}
	var ts int64
	err := tx.Raw("SELECT " + mainDatabaseUnixTimestampSQL()).Scan(&ts).Error
	if err != nil || ts <= 0 {
		return common.GetTimestamp()
	}
	return ts
}

func mainDatabaseUnixTimestampSQL() string {
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		// PostgreSQL's NOW() is fixed at the transaction start, unlike MySQL's
		// UNIX_TIMESTAMP() and SQLite's 'now'. statement_timestamp keeps lease
		// and lifecycle timestamps current during a long-running transaction,
		// while remaining stable within one statement on every dialect. Floor
		// also avoids PostgreSQL's numeric-to-bigint rounding.
		return "FLOOR(EXTRACT(EPOCH FROM statement_timestamp()))::bigint"
	case common.UsingMainDatabase(common.DatabaseTypeSQLite):
		return "CAST(strftime('%s','now') AS INTEGER)"
	default:
		return "UNIX_TIMESTAMP()"
	}
}
