package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type portableJSONTextColumn struct {
	table  string
	column string
}

var portableJSONTextColumns = []portableJSONTextColumn{
	{table: "channels", column: "channel_info"},
	{table: "prefill_groups", column: "items"},
	{table: "tasks", column: "properties"},
	{table: "tasks", column: "private_data"},
	{table: "tasks", column: "data"},
}

// migratePortableJSONColumnsToText prepares native JSON columns for the
// portable TEXT declarations used by the models. PostgreSQL requires an
// explicit USING cast; doing it before AutoMigrate also makes subsequent
// migrations idempotent. MySQL's native JSON type is converted explicitly so
// GORM does not repeatedly compare incompatible type names.
//
// SQLite's JSON declaration already has TEXT affinity. Its migrator rebuilds
// the affected table transactionally when AutoMigrate sees the new TEXT
// declaration, preserving both the values and the table's indexes.
func migratePortableJSONColumnsToText() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		return migratePostgreSQLJSONColumnsToText(DB)
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		return migrateMySQLJSONColumnsToText(DB)
	default:
		return nil
	}
}

func migratePostgreSQLJSONColumnsToText(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, target := range portableJSONTextColumns {
			var dataType string
			if err := tx.Raw(
				`SELECT data_type
				   FROM information_schema.columns
				  WHERE table_schema = current_schema()
				    AND table_name = ?
				    AND column_name = ?`,
				target.table,
				target.column,
			).Scan(&dataType).Error; err != nil {
				return fmt.Errorf(
					"inspect PostgreSQL JSON column %s.%s: %w",
					target.table,
					target.column,
					err,
				)
			}
			if dataType != "json" && dataType != "jsonb" {
				continue
			}
			statement := fmt.Sprintf(
				`ALTER TABLE "%s" ALTER COLUMN "%s" TYPE TEXT USING "%s"::text`,
				target.table,
				target.column,
				target.column,
			)
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf(
					"migrate PostgreSQL JSON column %s.%s to text: %w",
					target.table,
					target.column,
					err,
				)
			}
		}
		return nil
	})
}

func migrateMySQLJSONColumnsToText(db *gorm.DB) error {
	for _, target := range portableJSONTextColumns {
		var dataType string
		if err := db.Raw(
			`SELECT data_type
			   FROM information_schema.columns
			  WHERE table_schema = DATABASE()
			    AND table_name = ?
			    AND column_name = ?`,
			target.table,
			target.column,
		).Scan(&dataType).Error; err != nil {
			return fmt.Errorf(
				"inspect MySQL JSON column %s.%s: %w",
				target.table,
				target.column,
				err,
			)
		}
		if !strings.EqualFold(dataType, "json") {
			continue
		}
		statement := fmt.Sprintf(
			"ALTER TABLE `%s` MODIFY COLUMN `%s` TEXT NULL",
			target.table,
			target.column,
		)
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf(
				"migrate MySQL JSON column %s.%s to text: %w",
				target.table,
				target.column,
				err,
			)
		}
	}
	return nil
}
