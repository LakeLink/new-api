package model

import (
	"errors"
	"unicode/utf8"

	"gorm.io/gorm"
)

type CasbinRule struct {
	Id       uint    `gorm:"primaryKey;autoIncrement"`
	Ptype    string  `gorm:"size:16;index:idx_casbin_rule_lookup,priority:1"`
	V0       string  `gorm:"size:100;index:idx_casbin_rule_lookup,priority:2"`
	V1       string  `gorm:"size:100"`
	V2       string  `gorm:"size:100"`
	V3       string  `gorm:"size:100"`
	V4       string  `gorm:"size:100"`
	V5       string  `gorm:"size:100"`
	RuleHash *string `gorm:"type:char(64);uniqueIndex:ux_casbin_rule_hash"`
}

func (CasbinRule) TableName() string {
	return "casbin_rule"
}

func (rule *CasbinRule) BeforeCreate(_ *gorm.DB) error {
	if rule.Ptype == "" || utf8.RuneCountInString(rule.Ptype) > 16 {
		return errors.New("invalid Casbin policy type")
	}
	parts := []string{rule.Ptype, rule.V0, rule.V1, rule.V2, rule.V3, rule.V4, rule.V5}
	for _, value := range parts[1:] {
		if utf8.RuneCountInString(value) > 100 {
			return errors.New("Casbin policy value is too long")
		}
	}
	ruleHash := crossDatabaseIdentityHash(parts...)
	rule.RuleHash = &ruleHash
	return nil
}
