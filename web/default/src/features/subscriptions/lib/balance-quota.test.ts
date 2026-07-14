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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { calculateSubscriptionBalanceQuota } from './balance-quota.ts'

describe('subscription balance quota', () => {
  test('matches decimal ceiling at a binary floating-point integer boundary', () => {
    assert.equal(calculateSubscriptionBalanceQuota(0.000246, 500_000), 123)
    assert.equal(calculateSubscriptionBalanceQuota(0.000247, 500_000), 124)
  })

  test('supports decimal quota units and rejects invalid multipliers', () => {
    assert.equal(calculateSubscriptionBalanceQuota(0.14, 100.5), 15)
    assert.equal(calculateSubscriptionBalanceQuota(0, 500_000), 0)
    assert.equal(calculateSubscriptionBalanceQuota(-1, 500_000), null)
    assert.equal(calculateSubscriptionBalanceQuota(1, Number.NaN), null)
  })

  test('rejects charges outside the backend int32 quota range', () => {
    assert.equal(
      calculateSubscriptionBalanceQuota(4_294.967292, 500_000),
      2_147_483_646
    )
    assert.equal(calculateSubscriptionBalanceQuota(4_294.967294, 500_000), null)
  })
})
