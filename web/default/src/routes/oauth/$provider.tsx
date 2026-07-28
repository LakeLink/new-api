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
import {
  createFileRoute,
  useNavigate,
  useParams,
  useSearch,
} from '@tanstack/react-router'
import type { AxiosRequestConfig } from 'axios'
import i18next from 'i18next'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import { OAuthCallbackScreen } from '@/features/auth/components/oauth-callback-screen'
import {
  OAUTH_BIND_PENDING_STORAGE_KEY,
  OAUTH_BIND_STORAGE_KEY,
} from '@/features/auth/constants'
import { api, getSelf } from '@/lib/api'
import { normalizeInternalNavigationUrl } from '@/lib/safe-navigation'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

type OAuthRequestConfig = AxiosRequestConfig & {
  skipBusinessError?: boolean
}

const OAUTH_BIND_PENDING_TTL_MS = 10 * 60 * 1000

function hasPendingOAuthBinding(provider: string, state?: string): boolean {
  if (typeof window === 'undefined') return false
  try {
    const raw = window.localStorage.getItem(OAUTH_BIND_PENDING_STORAGE_KEY)
    if (!raw) return false
    const pending = JSON.parse(raw) as {
      provider?: string
      state?: string
      timestamp?: number
    }
    return (
      pending.provider === provider &&
      pending.state === state &&
      typeof pending.timestamp === 'number' &&
      Date.now() - pending.timestamp < OAUTH_BIND_PENDING_TTL_MS
    )
  } catch {
    return false
  }
}

function OAuthCallback() {
  const navigate = useNavigate()
  const { provider } = useParams({ from: '/oauth/$provider' }) as {
    provider: string
  }
  const search = useSearch({ from: '/oauth/$provider' }) as {
    code?: string
    state?: string
    redirect?: string
  }
  const [mode, setMode] = useState<'login' | 'bind'>(() =>
    hasPendingOAuthBinding(provider, search?.state) ? 'bind' : 'login'
  )

  useEffect(() => {
    if (typeof window === 'undefined') return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setMode(hasPendingOAuthBinding(provider, search?.state) ? 'bind' : 'login')
  }, [provider, search?.state])

  useEffect(() => {
    ;(async () => {
      const safeNavigate = (target: string) => {
        const safeTarget = normalizeInternalNavigationUrl(target, '/sign-in')
        navigate({ to: safeTarget as never, replace: true })
        if (typeof window !== 'undefined') {
          setTimeout(() => {
            const normalizedTarget = safeTarget
            const currentPath =
              window.location.pathname + window.location.search
            if (
              currentPath !== normalizedTarget &&
              currentPath !== `${normalizedTarget}/`
            ) {
              window.location.replace(safeTarget)
            }
          }, 100)
        }
      }

      if (!search?.code) {
        toast.error(i18next.t('Missing code'))
        safeNavigate('/sign-in')
        return
      }
      const isBindingFlow =
        hasPendingOAuthBinding(provider, search?.state) || mode === 'bind'
      if (isBindingFlow && mode !== 'bind') {
        setMode('bind')
      } else if (!isBindingFlow && mode !== 'login') {
        setMode('login')
      }
      const notifyBindingResult = (status: 'success' | 'error') => {
        if (typeof window === 'undefined') return
        try {
          window.localStorage.removeItem(OAUTH_BIND_PENDING_STORAGE_KEY)
          window.localStorage.setItem(
            OAUTH_BIND_STORAGE_KEY,
            JSON.stringify({
              provider,
              status,
              timestamp: Date.now(),
            })
          )
        } catch (_error) {
          // ignore storage write failures
          void _error
        }
      }

      const closeBindingWindow = () => {
        if (typeof window === 'undefined') return
        window.close()
        setTimeout(() => {
          if (!window.closed) {
            window.location.replace('/_authenticated/profile/')
          }
        }, 200)
      }

      const finalizeLogin = async (): Promise<boolean> => {
        try {
          const selfResponse = (await getSelf()) as {
            success?: boolean
            data?: AuthUser | null
          }
          if (selfResponse?.success && selfResponse.data) {
            useAuthStore.getState().auth.setUser(selfResponse.data)
            try {
              if (
                typeof window !== 'undefined' &&
                selfResponse.data?.id != null
              ) {
                window.localStorage.setItem('uid', String(selfResponse.data.id))
              }
            } catch (_error) {
              void _error
            }
            return true
          }
        } catch (_error) {
          void _error
        }
        return false
      }

      const redirectAfterLogin = (target?: string) => {
        const to = normalizeInternalNavigationUrl(
          target || search?.redirect,
          '/dashboard'
        )
        safeNavigate(to)
        toast.success(i18next.t('Signed in successfully!'))
      }

      const handleBindingFailure = (message: string) => {
        notifyBindingResult('error')
        toast.error(message)
      }

      const handleLoginFailure = async (message: string) => {
        if (await finalizeLogin()) {
          redirectAfterLogin()
          return
        }
        toast.error(message)
        safeNavigate('/sign-in')
      }

      try {
        const config: OAuthRequestConfig = {
          params: { code: search.code, state: search.state },
          skipBusinessError: true,
        }
        const res = await api.get(`/api/oauth/${provider}`, config)
        if (res?.data?.success) {
          const { message } = res.data
          const loginUser = (res.data?.data ?? null) as AuthUser | null
          // Check if this is a bind operation
          if (res.data?.data?.action === 'bind' || message === 'bind') {
            toast.success(i18next.t('Binding successful!'))
            notifyBindingResult('success')
            if (isBindingFlow) {
              // Close the callback window if we opened a new tab for binding
              closeBindingWindow()
            } else {
              safeNavigate('/_authenticated/profile/')
            }
            return
          }
          // Otherwise it's a login, use payload user if available
          if (loginUser) {
            useAuthStore.getState().auth.setUser(loginUser)
            try {
              if (typeof window !== 'undefined' && loginUser.id != null) {
                window.localStorage.setItem('uid', String(loginUser.id))
              }
            } catch (_error) {
              void _error
            }
            redirectAfterLogin()
            return
          }
          if (await finalizeLogin()) {
            redirectAfterLogin()
            return
          }
          toast.error(res?.data?.message || i18next.t('OAuth failed'))
          safeNavigate('/sign-in')
          return
        }
        const message = res?.data?.message || 'OAuth failed'
        if (!res?.data?.success && !isBindingFlow) {
          // When logging in with an already bound GitHub account, backend may return this message
          if (message === '该 GitHub 账户已被绑定') {
            if (await finalizeLogin()) {
              redirectAfterLogin()
              return
            }
          }
        }
        if (isBindingFlow) {
          handleBindingFailure(message)
        } else {
          await handleLoginFailure(message)
        }
        return
      } catch (error) {
        const message = ((error &&
          typeof error === 'object' &&
          'response' in error &&
          (error as { response?: { data?: { message?: string } } }).response
            ?.data?.message) ??
          (error instanceof Error ? error.message : undefined) ??
          'OAuth failed') as string

        if (isBindingFlow) {
          handleBindingFailure(message)
          return
        }
        await handleLoginFailure(message)
        return
      }
    })()
  }, [mode, navigate, provider, search])

  return <OAuthCallbackScreen provider={provider} mode={mode} />
}

export const Route = createFileRoute('/oauth/$provider')({
  component: OAuthCallback,
})
