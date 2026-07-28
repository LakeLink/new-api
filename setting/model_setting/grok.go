package model_setting

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/setting/config"
)

// GrokSettings defines Grok model configuration.
type GrokSettings struct {
	ViolationDeductionEnabled bool    `json:"violation_deduction_enabled"`
	ViolationDeductionAmount  float64 `json:"violation_deduction_amount"`
}

var defaultGrokSettings = GrokSettings{
	ViolationDeductionEnabled: true,
	ViolationDeductionAmount:  0.05,
}

var grokSettings = defaultGrokSettings

func (s GrokSettings) Validate() error {
	amount := s.ViolationDeductionAmount
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return fmt.Errorf("Grok violation deduction amount must be a finite non-negative number")
	}
	return nil
}

func init() {
	config.GlobalConfig.Register("grok", &grokSettings)
}

func GetGrokSettings() *GrokSettings {
	return config.Snapshot[GrokSettings]("grok")
}
