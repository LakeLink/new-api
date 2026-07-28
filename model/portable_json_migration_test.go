package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyJSONChannel struct {
	Id          int         `gorm:"primaryKey"`
	Key         string      `gorm:"not null"`
	ChannelInfo ChannelInfo `gorm:"type:json"`
}

func (legacyJSONChannel) TableName() string {
	return "channels"
}

type legacyJSONPrefillGroup struct {
	Id    int
	Name  string    `gorm:"size:64;not null"`
	Type  string    `gorm:"size:32;not null"`
	Items JSONValue `gorm:"type:json"`
}

func (legacyJSONPrefillGroup) TableName() string {
	return "prefill_groups"
}

type legacyJSONTask struct {
	ID          int64           `gorm:"primaryKey"`
	Properties  Properties      `gorm:"type:json"`
	PrivateData TaskPrivateData `gorm:"column:private_data;type:json"`
	Data        json.RawMessage `gorm:"type:json"`
}

func (legacyJSONTask) TableName() string {
	return "tasks"
}

func TestSQLiteJSONColumnsMigrateToTextWithoutDataLossOrSchemaChurn(t *testing.T) {
	db := useCrossDatabaseIdentityTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&legacyJSONChannel{},
		&legacyJSONPrefillGroup{},
		&legacyJSONTask{},
	))

	channelInfo := ChannelInfo{
		IsMultiKey:             true,
		MultiKeySize:           2,
		MultiKeyStatusList:     map[int]int{0: 1, 1: 2},
		MultiKeyDisabledReason: map[int]string{1: "rotated"},
	}
	require.NoError(t, db.Create(&legacyJSONChannel{
		Key:         "test-key",
		ChannelInfo: channelInfo,
	}).Error)
	require.NoError(t, db.Create(&legacyJSONPrefillGroup{
		Name:  "portable-json",
		Type:  "model",
		Items: JSONValue(`["model-a","model-b"]`),
	}).Error)
	require.NoError(t, db.Create(&legacyJSONTask{
		Properties: Properties{Input: "prompt", UpstreamModelName: "upstream"},
		PrivateData: TaskPrivateData{
			UpstreamTaskID: "provider-task",
			ResultURL:      "https://example.test/result",
		},
		Data: json.RawMessage(`{"status":"complete"}`),
	}).Error)

	require.NoError(t, prepareCrossDatabaseIdentityColumns())
	require.NoError(t, db.AutoMigrate(&Channel{}, &PrefillGroup{}, &Task{}))
	assertSQLiteColumnType(t, db, &Channel{}, "channel_info", "text")
	assertSQLiteColumnType(t, db, &PrefillGroup{}, "items", "text")
	assertSQLiteColumnType(t, db, &Task{}, "properties", "text")
	assertSQLiteColumnType(t, db, &Task{}, "private_data", "text")
	assertSQLiteColumnType(t, db, &Task{}, "data", "text")

	var channel Channel
	require.NoError(t, db.First(&channel).Error)
	assert.Equal(t, channelInfo, channel.ChannelInfo)
	var group PrefillGroup
	require.NoError(t, db.First(&group).Error)
	assert.JSONEq(t, `["model-a","model-b"]`, string(group.Items))
	var task Task
	require.NoError(t, db.First(&task).Error)
	assert.Equal(t, "prompt", task.Properties.Input)
	assert.Equal(t, "provider-task", task.PrivateData.UpstreamTaskID)
	assert.JSONEq(t, `{"status":"complete"}`, string(task.Data))

	before := sqliteTableDefinitions(t, db)
	require.NoError(t, db.AutoMigrate(&Channel{}, &PrefillGroup{}, &Task{}))
	after := sqliteTableDefinitions(t, db)
	assert.Equal(t, before, after)
}

func assertSQLiteColumnType(
	t *testing.T,
	db *gorm.DB,
	model any,
	columnName string,
	expectedType string,
) {
	t.Helper()
	columnTypes, err := db.Migrator().ColumnTypes(model)
	require.NoError(t, err)
	for _, columnType := range columnTypes {
		if columnType.Name() != columnName {
			continue
		}
		assert.True(
			t,
			strings.EqualFold(expectedType, columnType.DatabaseTypeName()),
			"column %s has database type %s",
			columnName,
			columnType.DatabaseTypeName(),
		)
		return
	}
	require.Failf(t, "SQLite column was not found", "column %s was not found", columnName)
}

type sqliteSchemaDefinition struct {
	Type      string
	Name      string
	TableName string `gorm:"column:tbl_name"`
	SQL       string
}

func sqliteTableDefinitions(t *testing.T, db *gorm.DB) []sqliteSchemaDefinition {
	t.Helper()
	var definitions []sqliteSchemaDefinition
	require.NoError(t, db.Raw(
		`SELECT type, name, tbl_name, sql
		   FROM sqlite_master
		  WHERE tbl_name IN ?
		    AND type IN ('table', 'index')
		    AND name NOT LIKE 'sqlite_%'
		  ORDER BY type, name`,
		[]string{"channels", "prefill_groups", "tasks"},
	).Scan(&definitions).Error)
	return definitions
}
