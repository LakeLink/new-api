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
import { STORAGE_KEYS } from '../../constants'

export type PlaygroundStorageKind = keyof typeof STORAGE_KEYS

const STORAGE_NAMESPACE = 'playground:v2'
const CLASSIC_STORAGE_NAMESPACE = 'playground:classic:v2'

export function getPlaygroundStorageKey(
  userId: number | null | undefined,
  kind: PlaygroundStorageKind
): string | null {
  if (
    typeof userId !== 'number' ||
    !Number.isSafeInteger(userId) ||
    userId <= 0
  ) {
    return null
  }
  return `${STORAGE_NAMESPACE}:user:${userId}:${kind.toLowerCase()}`
}

/**
 * Legacy keys had no owner. Migrating them into the currently signed-in
 * account could expose another person's conversation on a shared browser, so
 * they are deliberately discarded instead of guessed at.
 */
export function discardLegacyPlaygroundData(): void {
  try {
    for (const key of Object.values(STORAGE_KEYS)) {
      window.localStorage.removeItem(key)
    }
  } catch {
    // Storage can be unavailable in private browsing or restrictive contexts.
  }
}

export function clearPlaygroundData(userId: number | null | undefined): void {
  try {
    for (const kind of Object.keys(STORAGE_KEYS) as PlaygroundStorageKind[]) {
      const key = getPlaygroundStorageKey(userId, kind)
      if (key) {
        window.localStorage.removeItem(key)
      }
    }
    for (const kind of ['config', 'messages']) {
      if (
        typeof userId === 'number' &&
        Number.isSafeInteger(userId) &&
        userId > 0
      ) {
        window.localStorage.removeItem(
          `${CLASSIC_STORAGE_NAMESPACE}:user:${userId}:${kind}`
        )
      }
    }
    discardLegacyPlaygroundData()
  } catch {
    // Best-effort local cleanup; the server session is cleared independently.
  }
}
