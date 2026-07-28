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
import { create } from 'zustand'

import { clearPlaygroundData } from '@/features/playground/lib/storage/playground-storage'
import type { AdminCapabilities } from '@/lib/admin-permissions'

export type UserPermissions = {
  sidebar_settings?: boolean
  sidebar_modules?: Record<string, unknown>
  admin_permissions?: AdminCapabilities
}

export interface AuthUser {
  id: number
  username: string
  display_name?: string
  email?: string
  role: number
  status?: number
  group?: string
  quota?: number
  used_quota?: number
  request_count?: number
  aff_code?: string
  aff_count?: number
  aff_quota?: number
  aff_history_quota?: number
  inviter_id?: number
  github_id?: string
  oidc_id?: string
  wechat_id?: string
  telegram_id?: string
  linux_do_id?: string
  setting?: Record<string, unknown> | string
  stripe_customer?: string
  sidebar_modules?: string
  permissions?: UserPermissions
}

interface AuthState {
  auth: {
    user: AuthUser | null
    setUser: (user: AuthUser | null) => void
    reset: () => void
  }
}

const SENSITIVE_SETTING_KEYS = new Set(['gotify_token', 'webhook_secret'])

function sanitizeSettingForStorage(
  setting: AuthUser['setting']
): AuthUser['setting'] {
  if (!setting) {
    return setting
  }

  let parsed: Record<string, unknown>
  try {
    parsed =
      typeof setting === 'string'
        ? (JSON.parse(setting) as Record<string, unknown>)
        : { ...setting }
  } catch {
    return undefined
  }

  SENSITIVE_SETTING_KEYS.forEach((key) => delete parsed[key])
  return typeof setting === 'string' ? JSON.stringify(parsed) : parsed
}

function sanitizeUserForStorage(user: AuthUser): AuthUser {
  return {
    ...user,
    setting: sanitizeSettingForStorage(user.setting),
  }
}

export const useAuthStore = create<AuthState>()((set) => {
  // Restore user info from localStorage
  const initUser = (() => {
    try {
      if (typeof window !== 'undefined') {
        const saved = window.localStorage.getItem('user')
        if (!saved) {
          return null
        }
        const user = sanitizeUserForStorage(JSON.parse(saved) as AuthUser)
        window.localStorage.setItem('user', JSON.stringify(user))
        return user
      }
    } catch {
      // Clear dirty data when parsing fails
      if (typeof window !== 'undefined') {
        try {
          window.localStorage.removeItem('user')
        } catch {
          // Storage can be unavailable in restrictive browser contexts.
        }
      }
    }
    return null
  })()

  return {
    auth: {
      user: initUser,
      setUser: (user) =>
        set((state) => {
          // Persist user to localStorage
          if (typeof window !== 'undefined') {
            try {
              if (user) {
                window.localStorage.setItem(
                  'user',
                  JSON.stringify(sanitizeUserForStorage(user))
                )
              } else {
                window.localStorage.removeItem('user')
              }
            } catch {
              // Authentication state must still update when storage is blocked.
            }
          }
          return { ...state, auth: { ...state.auth, user } }
        }),
      reset: () =>
        set((state) => {
          if (typeof window !== 'undefined') {
            clearPlaygroundData(state.auth.user?.id)
            try {
              window.localStorage.removeItem('user')
            } catch {
              // The in-memory session reset remains authoritative.
            }
          }
          return {
            ...state,
            auth: { ...state.auth, user: null },
          }
        }),
    },
  }
})
