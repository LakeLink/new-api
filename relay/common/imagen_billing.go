package common

import (
	"errors"
	"fmt"

	basecommon "github.com/QuantumNous/new-api/common"
)

const (
	MaxImagenImageCount  = 4
	ImagenTokensPerImage = 258
)

// SyncImagenBilling aligns pre-consume state with an Imagen predict request.
// Fixed-price models charge per requested image, while ratio-priced models
// already report 258 prompt tokens per generated image and therefore must not
// retain an additional n multiplier for settlement.
func SyncImagenBilling(info *RelayInfo, imageCount int) error {
	if info == nil {
		return errors.New("relay info is required for Imagen billing")
	}
	if imageCount < 1 || imageCount > MaxImagenImageCount {
		return fmt.Errorf("Imagen image count must be between 1 and %d", MaxImagenImageCount)
	}

	var quotaToReserve int
	var err error
	if info.PriceData.UsePrice {
		info.PriceData.AddOtherRatio("n", float64(imageCount))
		quotaToReserve, err = basecommon.QuotaRoundStrict(info.PriceData.ApplyOtherRatiosToFloat(
			info.PriceData.ModelPrice * basecommon.CurrentQuotaPerUnit() * info.PriceData.GroupRatioInfo.GroupRatio,
		))
	} else {
		info.PriceData.RemoveOtherRatio("n")
		quotaToReserve, err = basecommon.QuotaRoundStrict(info.PriceData.ApplyOtherRatiosToFloat(
			float64(ImagenTokensPerImage*imageCount) * info.PriceData.ModelRatio * info.PriceData.GroupRatioInfo.GroupRatio,
		))
	}
	if err != nil {
		return fmt.Errorf("calculate Imagen reservation: %w", err)
	}
	if info.PriceData.FreeModel || quotaToReserve <= info.PriceData.QuotaToPreConsume {
		return nil
	}
	if info.Billing == nil {
		return errors.New("billing reservation is unavailable for Imagen request")
	}
	if err := info.Billing.Reserve(quotaToReserve); err != nil {
		return fmt.Errorf("reserve Imagen output quota: %w", err)
	}
	info.PriceData.QuotaToPreConsume = quotaToReserve
	return nil
}
