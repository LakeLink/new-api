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
import React, { useState, useCallback, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import useDialogState from '@/hooks/use-dialog'
import { isVerificationRequiredError } from '@/lib/secure-verification'

import { fetchTokenKey, fetchTokenKeysBatch } from '../api'
import { ERROR_MESSAGES } from '../constants'
import type { ApiKey, ApiKeysDialogType } from '../types'

type ApiKeysContextType = {
  open: ApiKeysDialogType | null
  setOpen: (str: ApiKeysDialogType | null) => void
  currentRow: ApiKey | null
  setCurrentRow: React.Dispatch<React.SetStateAction<ApiKey | null>>
  refreshTrigger: number
  triggerRefresh: () => void
  resolvedKey: string
  setResolvedKey: React.Dispatch<React.SetStateAction<string>>
  resolveRealKey: (id: number) => Promise<string | null>
  resolveRealKeysBatch: (ids: number[]) => Promise<Record<number, string>>
  resolvedKeys: Record<number, string>
  loadingKeys: Record<number, boolean>
  copiedKeyId: number | null
  markKeyCopied: (id: number) => void
}

const ApiKeysContext = React.createContext<ApiKeysContextType | null>(null)

export function ApiKeysProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const {
    open: verificationOpen,
    methods: verificationMethods,
    state: verificationState,
    executeVerification,
    cancel: cancelVerification,
    setCode: setVerificationCode,
    switchMethod: switchVerificationMethod,
    withVerification,
  } = useSecureVerification()
  const [open, setOpen] = useDialogState<ApiKeysDialogType>(null)
  const [currentRow, setCurrentRow] = useState<ApiKey | null>(null)
  const [refreshTrigger, setRefreshTrigger] = useState(0)
  const [resolvedKey, setResolvedKey] = useState('')

  const [resolvedKeys, setResolvedKeys] = useState<Record<number, string>>({})
  const [loadingKeys, setLoadingKeys] = useState<Record<number, boolean>>({})
  const pendingRequests = useRef<Record<number, Promise<string | null>>>({})

  const [copiedKeyId, setCopiedKeyId] = useState<number | null>(null)
  const copiedTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined)

  useEffect(() => {
    return () => clearTimeout(copiedTimerRef.current)
  }, [])

  const markKeyCopied = useCallback((id: number) => {
    setCopiedKeyId(id)
    clearTimeout(copiedTimerRef.current)
    copiedTimerRef.current = setTimeout(() => setCopiedKeyId(null), 2000)
  }, [])

  const triggerRefresh = useCallback(() => {
    setRefreshTrigger((prev) => prev + 1)
  }, [])

  const fetchAndCacheRealKey = useCallback(
    async (id: number): Promise<string | null> => {
      const res = await fetchTokenKey(id)
      if (res.success && res.data?.key) {
        const fullKey = `sk-${res.data.key}`
        setResolvedKeys((prev) => ({ ...prev, [id]: fullKey }))
        return fullKey
      }
      toast.error(res.message || t(ERROR_MESSAGES.UNEXPECTED))
      return null
    },
    [t]
  )

  const resolveRealKey = useCallback(
    async (id: number): Promise<string | null> => {
      if (resolvedKeys[id]) return resolvedKeys[id]
      if (id in pendingRequests.current) return pendingRequests.current[id]

      const request = (async () => {
        setLoadingKeys((prev) => ({ ...prev, [id]: true }))
        try {
          return await fetchAndCacheRealKey(id)
        } catch (error) {
          if (isVerificationRequiredError(error)) {
            try {
              const result = await withVerification(
                () => fetchAndCacheRealKey(id),
                {
                  title: t('Security verification'),
                  description: t(
                    'Confirm your identity before accessing this sensitive action.'
                  ),
                  scope: 'channel.key.read',
                  preferredMethod: 'passkey',
                }
              )
              return typeof result === 'string' ? result : null
            } catch {
              return null
            }
          }

          toast.error(t(ERROR_MESSAGES.UNEXPECTED))
          return null
        } finally {
          delete pendingRequests.current[id]
          setLoadingKeys((prev) => {
            const next = { ...prev }
            delete next[id]
            return next
          })
        }
      })()

      pendingRequests.current[id] = request
      return request
    },
    [fetchAndCacheRealKey, resolvedKeys, t, withVerification]
  )

  const fetchAndCacheRealKeysBatch = useCallback(
    async (ids: number[]): Promise<Record<number, string>> => {
      const res = await fetchTokenKeysBatch(ids)
      if (res.success && res.data?.keys) {
        const newKeys: Record<number, string> = {}
        for (const [idStr, key] of Object.entries(res.data.keys)) {
          newKeys[Number(idStr)] = `sk-${key}`
        }
        setResolvedKeys((prev) => ({ ...prev, ...newKeys }))
        return newKeys
      }
      toast.error(res.message || t(ERROR_MESSAGES.UNEXPECTED))
      return {}
    },
    [t]
  )

  const resolveRealKeysBatch = useCallback(
    async (ids: number[]): Promise<Record<number, string>> => {
      const uncachedIds = ids.filter((id) => !resolvedKeys[id])
      if (uncachedIds.length === 0) {
        const result: Record<number, string> = {}
        for (const id of ids) result[id] = resolvedKeys[id]
        return result
      }

      for (const id of uncachedIds) {
        setLoadingKeys((prev) => ({ ...prev, [id]: true }))
      }

      try {
        const newKeys = await fetchAndCacheRealKeysBatch(uncachedIds)
        const result: Record<number, string> = { ...newKeys }
        for (const id of ids) {
          if (resolvedKeys[id]) result[id] = resolvedKeys[id]
        }
        return result
      } catch (error) {
        if (isVerificationRequiredError(error)) {
          const result = await withVerification(
            () => fetchAndCacheRealKeysBatch(uncachedIds),
            {
              title: t('Security verification'),
              description: t(
                'Confirm your identity before accessing this sensitive action.'
              ),
              scope: 'channel.key.read',
              preferredMethod: 'passkey',
            }
          )
          if (!result || typeof result !== 'object') return {}

          return {
            ...(result as Record<number, string>),
            ...Object.fromEntries(
              ids
                .filter((id) => resolvedKeys[id])
                .map((id) => [id, resolvedKeys[id]])
            ),
          }
        }

        toast.error(t(ERROR_MESSAGES.UNEXPECTED))
        return {}
      } finally {
        for (const id of uncachedIds) {
          setLoadingKeys((prev) => {
            const next = { ...prev }
            delete next[id]
            return next
          })
        }
      }
    },
    [fetchAndCacheRealKeysBatch, resolvedKeys, t, withVerification]
  )

  return (
    <>
      <ApiKeysContext
        value={{
          open,
          setOpen,
          currentRow,
          setCurrentRow,
          refreshTrigger,
          triggerRefresh,
          resolvedKey,
          setResolvedKey,
          resolveRealKey,
          resolveRealKeysBatch,
          resolvedKeys,
          loadingKeys,
          copiedKeyId,
          markKeyCopied,
        }}
      >
        {children}
      </ApiKeysContext>
      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) cancelVerification()
        }}
        methods={verificationMethods}
        state={verificationState}
        onVerify={async (method, code) => {
          try {
            await executeVerification(method, code)
          } catch {
            // Errors are already surfaced by useSecureVerification.
          }
        }}
        onCancel={cancelVerification}
        onCodeChange={setVerificationCode}
        onMethodChange={switchVerificationMethod}
      />
    </>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useApiKeys = () => {
  const apiKeysContext = React.useContext(ApiKeysContext)

  if (!apiKeysContext) {
    throw new Error('useApiKeys has to be used within <ApiKeysContext>')
  }

  return apiKeysContext
}
