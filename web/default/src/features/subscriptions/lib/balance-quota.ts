/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/**
 * Matches the backend's decimal multiplication and ceiling for balance-plan
 * redemption. Integer arithmetic avoids a one-quota overstatement when a
 * decimal price lands just above an integer in binary floating point. A null
 * result mirrors backend rejection when the charge is invalid or exceeds the
 * signed 32-bit quota range.
 */
export function calculateSubscriptionBalanceQuota(
  priceAmount: number,
  quotaPerUnit: number
): number | null {
  if (
    !Number.isFinite(priceAmount) ||
    !Number.isFinite(quotaPerUnit) ||
    priceAmount < 0 ||
    quotaPerUnit <= 0
  ) {
    return null
  }
  if (priceAmount === 0) {
    return 0
  }

  const price = getDecimalParts(priceAmount)
  const quotaUnit = getDecimalParts(quotaPerUnit)
  const scale = price.scale + quotaUnit.scale
  const denominator = 10n ** BigInt(scale)
  const product = price.digits * quotaUnit.digits
  const quota = (product + denominator - 1n) / denominator

  if (quota >= 2_147_483_647n) {
    return null
  }
  return Number(quota)
}

function getDecimalParts(value: number): { digits: bigint; scale: number } {
  const [coefficient, exponentText] = value.toString().toLowerCase().split('e')
  const exponent = exponentText ? Number(exponentText) : 0
  const [whole, fraction = ''] = coefficient.split('.')
  let digits = BigInt(`${whole}${fraction}`)
  let scale = fraction.length - exponent

  if (scale < 0) {
    digits *= 10n ** BigInt(-scale)
    scale = 0
  }

  return { digits, scale }
}
