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
import test from 'node:test';

import { buildLogFilterExpression } from './log-expression.js';

test('builds an equivalent expression for every Classic field filter', () => {
  const expression = buildLogFilterExpression(
    {
      dateRange: [
        new Date('2026-07-12T00:00:00.000Z'),
        new Date('2026-07-12T23:59:59.000Z'),
      ],
      logType: '5',
      token_name: 'main "token"',
      model_name: 'gpt',
      group: 'vip',
      request_id: 'req_1',
      upstream_request_id: 'up_1',
      username: 'alice',
      channel: '12',
    },
    true,
  );

  assert.equal(
    expression,
    'created_at >= 1783814400 && created_at <= 1783900799' +
      ' && type == 5' +
      ' && token_name contains "main \\"token\\""' +
      ' && model_name contains "gpt"' +
      ' && group == "vip"' +
      ' && request_id == "req_1"' +
      ' && upstream_request_id == "up_1"' +
      ' && username contains "alice"' +
      ' && channel == 12',
  );
});

test('omits empty, invalid, and admin-only values for self users', () => {
  assert.equal(
    buildLogFilterExpression(
      {
        dateRange: [],
        logType: '0',
        token_name: ' ',
        username: 'alice',
        channel: 'not-a-number',
      },
      false,
    ),
    '',
  );
});

test('preserves non-zero numeric filters exactly', () => {
  assert.equal(
    buildLogFilterExpression(
      {
        dateRange: [],
        logType: '-1',
        channel: '-12',
      },
      true,
    ),
    'type == -1 && channel == -12',
  );
});

test('matches the field API integer parsing for channel filters', () => {
  assert.equal(
    buildLogFilterExpression(
      {
        dateRange: [],
        channel: ' 12 ',
      },
      true,
    ),
    '',
  );
  assert.equal(
    buildLogFilterExpression(
      {
        dateRange: [],
        channel: '1e2',
      },
      true,
    ),
    '',
  );
  assert.equal(
    buildLogFilterExpression(
      {
        dateRange: [],
        channel: '+12',
      },
      true,
    ),
    'channel == 12',
  );
});

test('uses the field API fallback range when the picker is cleared', () => {
  assert.equal(
    buildLogFilterExpression({ dateRange: [] }, false, [
      new Date('2026-07-12T00:00:00.000Z'),
      new Date('2026-07-12T01:00:00.000Z'),
    ]),
    'created_at >= 1783814400 && created_at <= 1783818000',
  );
});
