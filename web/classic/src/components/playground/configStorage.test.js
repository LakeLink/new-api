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
import { beforeEach, describe, test } from 'node:test';

import {
  clearPlaygroundData,
  loadMessages,
  saveMessages,
} from './configStorage.js';

class MemoryStorage {
  constructor() {
    this.values = new Map();
  }

  get length() {
    return this.values.size;
  }

  clear() {
    this.values.clear();
  }

  getItem(key) {
    return this.values.get(key) ?? null;
  }

  key(index) {
    return [...this.values.keys()][index] ?? null;
  }

  removeItem(key) {
    this.values.delete(key);
  }

  setItem(key, value) {
    this.values.set(key, String(value));
  }
}

const storage = new MemoryStorage();
Object.defineProperty(globalThis, 'localStorage', { value: storage });

function setCurrentUser(id) {
  storage.setItem('user', JSON.stringify({ id }));
}

describe('classic playground local storage isolation', () => {
  beforeEach(() => {
    storage.clear();
  });

  test('keeps conversations scoped to the current user', () => {
    setCurrentUser(11);
    saveMessages([{ id: 'secret', content: 'private prompt' }]);

    setCurrentUser(22);
    assert.equal(loadMessages(), null);

    setCurrentUser(11);
    assert.equal(loadMessages()?.[0]?.content, 'private prompt');
  });

  test('discards ownerless legacy conversations', () => {
    setCurrentUser(11);
    storage.setItem(
      'playground_messages',
      JSON.stringify({ messages: [{ content: 'unknown owner' }] }),
    );

    assert.equal(loadMessages(), null);
    assert.equal(storage.getItem('playground_messages'), null);
  });

  test('sign-out cleanup removes both theme namespaces for one account', () => {
    setCurrentUser(11);
    saveMessages([{ id: 'classic', content: 'classic prompt' }]);
    storage.setItem(
      'playground:v2:user:11:messages',
      JSON.stringify({ version: 2, data: [] }),
    );

    clearPlaygroundData(11);

    assert.equal(loadMessages(), null);
    assert.equal(storage.getItem('playground:v2:user:11:messages'), null);
  });
});
