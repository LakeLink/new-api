package operation_setting

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type PaymentSetting struct {
	AmountOptions  []int           `json:"amount_options"`
	AmountDiscount map[int]float64 `json:"amount_discount"` // 充值金额对应的折扣，例如 100 元 0.9 表示 100 元充值享受 9 折优惠
}

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:  []int{10, 20, 50, 100, 200, 500},
	AmountDiscount: map[int]float64{},
}

func (s PaymentSetting) Validate() error {
	if len(s.AmountOptions) > 1_000 {
		return fmt.Errorf("amount options cannot contain more than 1000 entries")
	}
	seen := make(map[int]struct{}, len(s.AmountOptions))
	for _, amount := range s.AmountOptions {
		if amount < 1 || amount > common.MaxQuota {
			return fmt.Errorf("amount option %d must be in the range [1, %d]", amount, common.MaxQuota)
		}
		if _, ok := seen[amount]; ok {
			return fmt.Errorf("amount option %d is duplicated", amount)
		}
		seen[amount] = struct{}{}
	}
	for amount, discount := range s.AmountDiscount {
		if amount < 1 || amount > common.MaxQuota {
			return fmt.Errorf("discount amount %d must be in the range [1, %d]", amount, common.MaxQuota)
		}
		if math.IsNaN(discount) || math.IsInf(discount, 0) || discount <= 0 || discount > 1 {
			return fmt.Errorf("discount for amount %d must be finite and in the range (0, 1]", amount)
		}
	}
	return nil
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return config.Snapshot[PaymentSetting]("payment_setting")
}
