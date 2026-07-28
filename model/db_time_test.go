package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestMainDatabaseUnixTimestampSQLUsesPortableWallClockSemantics(t *testing.T) {
	originalType := common.MainDatabaseType()
	t.Cleanup(func() {
		common.SetMainDatabaseType(originalType)
	})

	tests := []struct {
		name         string
		databaseType common.DatabaseType
		expected     string
	}{
		{
			name:         "PostgreSQL",
			databaseType: common.DatabaseTypePostgreSQL,
			expected:     "FLOOR(EXTRACT(EPOCH FROM statement_timestamp()))::bigint",
		},
		{
			name:         "SQLite",
			databaseType: common.DatabaseTypeSQLite,
			expected:     "CAST(strftime('%s','now') AS INTEGER)",
		},
		{
			name:         "MySQL",
			databaseType: common.DatabaseTypeMySQL,
			expected:     "UNIX_TIMESTAMP()",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			common.SetMainDatabaseType(test.databaseType)
			assert.Equal(t, test.expected, mainDatabaseUnixTimestampSQL())
		})
	}
}
