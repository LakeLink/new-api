package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetLogExprSchemaScopesFieldsAndExamples(t *testing.T) {
	userSchema := GetLogExprSchema(false)
	adminSchema := GetLogExprSchema(true)

	assert.Equal(t, maxLogExprLength, userSchema.Limits.MaxLength)
	assert.Equal(t, maxLogExprNodes, userSchema.Limits.MaxNodes)
	assert.Equal(t, maxLogExprStringLength, userSchema.Limits.MaxStringLength)
	assert.Equal(t, maxLogExprInItems, userSchema.Limits.MaxInItems)
	require.NotEmpty(t, userSchema.Operators)
	require.NotEmpty(t, userSchema.Examples)
	assert.Equal(t, "Expression search is parsed from the AST and translated into SQL with an allowed field list, placeholders, and escaped LIKE patterns.", userSchema.Intro)
	assert.Equal(t, "表达式搜索会从 AST 解析表达式，并使用允许字段列表、SQL 参数占位符和转义后的 LIKE 模式生成查询。", userSchema.IntroZh)
	assert.Contains(t, userSchema.QuickSyntax, "Boolean fields can be written directly")
	assert.Contains(t, userSchema.QuickSyntaxZh, "布尔字段可以直接写")
	assert.Contains(t, userSchema.Safety, "field-to-field comparisons are unsupported")
	assert.Contains(t, userSchema.SafetyZh, "不支持正则表达式、算术运算、任意函数和字段间比较")
	require.NotEmpty(t, userSchema.Fields)
	assert.Equal(t, "Number", userSchema.Fields[0].Type)
	assert.Equal(t, "Combine conditions with boolean logic and parentheses.", userSchema.Operators[0].Description)
	assert.Equal(t, "使用布尔逻辑和括号组合多个条件。", userSchema.Operators[0].DescriptionZh)
	assert.Equal(t, "GPT consumption logs", userSchema.Examples[0].Title)
	assert.Equal(t, "GPT 消费日志", userSchema.Examples[0].TitleZh)
	var typeField *LogExprFieldDocumentation
	for i := range userSchema.Fields {
		if userSchema.Fields[i].Names[0] == "type" {
			typeField = &userSchema.Fields[i]
			break
		}
	}
	require.NotNil(t, typeField)
	assert.Contains(t, typeField.Description, "7 login")
	assert.Contains(t, typeField.DescriptionZh, "7 登录")

	for _, field := range userSchema.Fields {
		assert.Equal(t, "all", field.Scope)
		assert.NotEmpty(t, field.Names)
		assert.NotEmpty(t, field.Description)
	}
	for _, example := range userSchema.Examples {
		assert.NotEqual(t, "admin", example.Scope)
	}

	assert.True(t, schemaHasLogExprField(adminSchema, "channel_name", "admin"))
	assert.True(t, schemaHasLogExprField(adminSchema, "other", "admin"))
	assert.True(t, schemaHasLogExprField(adminSchema, "id", "admin"))
	assert.False(t, schemaHasLogExprField(userSchema, "channel_name", "admin"))
	assert.False(t, schemaHasLogExprField(userSchema, "other", "admin"))
	assert.False(t, schemaHasLogExprField(userSchema, "id", "admin"))
	for _, field := range adminSchema.Fields {
		if field.Names[0] == "id" {
			assert.Equal(t, "Log record ID.", field.Description)
			assert.Equal(t, "日志记录 ID。", field.DescriptionZh)
		}
	}
}

func TestGetLogExprSchemaOmitsDisplayOnlyIDForClickHouse(t *testing.T) {
	originalDatabaseType := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetLogDatabaseType(originalDatabaseType)
	})
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)

	schema := GetLogExprSchema(true)

	assert.False(t, schemaHasLogExprField(schema, "id", "all"))
	assert.False(t, schemaHasLogExprField(schema, "id", "admin"))
}

func TestGetLogExprSchemaOmitsChannelNameWithSeparateLogDatabase(t *testing.T) {
	oldDB := DB
	oldLogDB := LOG_DB
	DB = &gorm.DB{}
	LOG_DB = &gorm.DB{}
	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
	})

	schema := GetLogExprSchema(true)

	assert.False(t, schemaHasLogExprField(schema, "channel_name", "admin"))
	for _, example := range schema.Examples {
		assert.NotEqual(t, "Admin: channel name", example.Title)
	}
}

func schemaHasLogExprField(schema LogExprSchema, name string, scope string) bool {
	for _, field := range schema.Fields {
		if field.Scope != scope {
			continue
		}
		for _, alias := range field.Names {
			if alias == name {
				return true
			}
		}
	}
	return false
}
