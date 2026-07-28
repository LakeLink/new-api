/*
Copyright (C) 2025 QuantumNous

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

import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import { parseWebChatPresets, resolveWebChatUrl } from './chatLinks.js';

describe('Classic web chat links', () => {
  test('filters active and malformed protocols from stored chat settings', () => {
    const presets = parseWebChatPresets([
      { Unsafe: 'javascript:alert(1)' },
      { App: 'deepchat://provider/install' },
      { Web: 'https://chat.example.test/?key={key}' },
    ]);

    assert.deepEqual(presets, [
      { index: 2, template: 'https://chat.example.test/?key={key}' },
    ]);
  });

  test('resolves placeholders and preserves explicit sk- prefixes', () => {
    assert.equal(
      resolveWebChatUrl(
        'https://chat.example.test/?key={key}&base={address}',
        'sk-secret',
        'https://gateway.example.test',
      ),
      'https://chat.example.test/?key=sk-secret&base=https%3A%2F%2Fgateway.example.test',
    );
  });
});
