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
import { beforeEach, describe, test } from 'node:test'

import { STORAGE_KEYS } from '../../constants.ts'
import type { Message } from '../../types.ts'
import {
  clearPlaygroundData,
  getPlaygroundStorageKey,
} from './playground-storage.ts'
import {
  loadConfig,
  loadMessages,
  saveConfig,
  saveMessages,
} from './storage.ts'

class MemoryStorage implements Storage {
  private readonly values = new Map<string, string>()

  get length(): number {
    return this.values.size
  }

  clear(): void {
    this.values.clear()
  }

  getItem(key: string): string | null {
    return this.values.get(key) ?? null
  }

  key(index: number): string | null {
    return [...this.values.keys()][index] ?? null
  }

  removeItem(key: string): void {
    this.values.delete(key)
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value)
  }
}

const storage = new MemoryStorage()
Object.defineProperty(globalThis, 'localStorage', { value: storage })
Object.defineProperty(globalThis, 'window', { value: globalThis })

function message(key: string, content: string): Message {
  return {
    key,
    from: 'user',
    versions: [{ id: `${key}-v1`, content }],
  }
}

describe('playground local storage isolation', () => {
  beforeEach(() => {
    storage.clear()
  })

  test('keeps configuration and conversations scoped to one user', () => {
    saveConfig(11, { model: 'private-model' })
    saveMessages(11, [message('secret', 'private prompt')])

    assert.equal(loadConfig(11).model, 'private-model')
    assert.equal(loadMessages(11)?.[0]?.versions[0]?.content, 'private prompt')
    assert.deepEqual(loadConfig(22), {})
    assert.equal(loadMessages(22), null)
    assert.notEqual(
      getPlaygroundStorageKey(11, 'MESSAGES'),
      getPlaygroundStorageKey(22, 'MESSAGES')
    )
  })

  test('discards ownerless legacy history instead of assigning it to a user', () => {
    storage.setItem(
      STORAGE_KEYS.MESSAGES,
      JSON.stringify([message('legacy', 'unknown owner')])
    )

    assert.equal(loadMessages(11), null)
    assert.equal(storage.getItem(STORAGE_KEYS.MESSAGES), null)
  })

  test('sign-out cleanup removes only the current scoped account', () => {
    saveMessages(11, [message('first', 'first account')])
    saveMessages(22, [message('second', 'second account')])
    storage.setItem(
      'playground:classic:v2:user:11:messages',
      JSON.stringify({ messages: [message('classic', 'classic account')] })
    )

    clearPlaygroundData(11)

    assert.equal(loadMessages(11), null)
    assert.equal(loadMessages(22)?.[0]?.versions[0]?.content, 'second account')
    assert.equal(
      storage.getItem('playground:classic:v2:user:11:messages'),
      null
    )
  })

  test('auth reset clears the signed-in account before removing its identity', async () => {
    const { useAuthStore } = await import('../../../../stores/auth-store.ts')
    useAuthStore.getState().auth.setUser({
      id: 11,
      username: 'private-user',
      role: 1,
    })
    saveMessages(11, [message('secret', 'private prompt')])

    useAuthStore.getState().auth.reset()

    assert.equal(useAuthStore.getState().auth.user, null)
    assert.equal(loadMessages(11), null)
  })
})
