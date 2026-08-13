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

import {
  normalizeContentLinkUrl,
  normalizeExternalProtocolUrl,
  normalizeHttpUrl,
  normalizeInternalNavigationUrl,
} from './safe-navigation.ts'

describe('safe navigation URLs', () => {
  test('keeps internal redirects on the current application origin', () => {
    assert.equal(
      normalizeInternalNavigationUrl('/keys?status=active#top'),
      '/keys?status=active#top'
    )
    assert.equal(
      normalizeInternalNavigationUrl('https://attacker.example/steal'),
      '/dashboard'
    )
    assert.equal(
      normalizeInternalNavigationUrl('//attacker.example/steal'),
      '/dashboard'
    )
    assert.equal(
      normalizeInternalNavigationUrl('javascript:alert(1)'),
      '/dashboard'
    )
  })

  test('accepts only credential-free HTTP URLs for web navigation', () => {
    assert.equal(
      normalizeHttpUrl('https://example.com/docs'),
      'https://example.com/docs'
    )
    assert.equal(normalizeHttpUrl('https://user:secret@example.com'), null)
    assert.equal(normalizeHttpUrl('javascript:alert(1)'), null)
  })

  test('blocks active browser protocols while preserving app protocols', () => {
    assert.equal(
      normalizeExternalProtocolUrl('ccswitch://import?name=test'),
      'ccswitch://import?name=test'
    )
    assert.equal(normalizeExternalProtocolUrl('data:text/html,hello'), null)
    assert.equal(normalizeExternalProtocolUrl('javascript:alert(1)'), null)
  })

  test('limits rendered content links to navigation-safe protocols', () => {
    assert.equal(normalizeContentLinkUrl('/docs#models'), '/docs#models')
    assert.equal(normalizeContentLinkUrl('#usage'), '#usage')
    assert.equal(
      normalizeContentLinkUrl('mailto:support@example.com'),
      'mailto:support@example.com'
    )
    assert.equal(normalizeContentLinkUrl('ccswitch://import'), null)
    assert.equal(normalizeContentLinkUrl('data:text/html,hello'), null)
  })
})
