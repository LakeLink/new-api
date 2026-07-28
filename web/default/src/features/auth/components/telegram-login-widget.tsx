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
import { Loader2, Send } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import type { TelegramAuthPayload } from '../types'

const TELEGRAM_WIDGET_SRC = 'https://telegram.org/js/telegram-widget.js?22'
const TELEGRAM_CALLBACK_NAME = '__newApiTelegramWidgetAuth'
const telegramCallbacks = new Map<string, (payload: unknown) => void>()

let callbackSequence = 0
let callbackRegistrationCount = 0
let previousTelegramCallback:
  | ((callbackId: string, payload: unknown) => void)
  | undefined

declare global {
  interface Window {
    __newApiTelegramWidgetAuth?: (callbackId: string, payload: unknown) => void
  }
}

function dispatchTelegramAuthorization(
  callbackId: string,
  payload: unknown
): void {
  telegramCallbacks.get(callbackId)?.(payload)
}

function registerTelegramCallback(callback: (payload: unknown) => void): {
  callbackId: string
  unregister: () => void
} {
  callbackSequence += 1
  const callbackId = `telegram_widget_${callbackSequence}`

  if (callbackRegistrationCount === 0) {
    previousTelegramCallback = window.__newApiTelegramWidgetAuth
    window.__newApiTelegramWidgetAuth = dispatchTelegramAuthorization
  }

  callbackRegistrationCount += 1
  telegramCallbacks.set(callbackId, callback)

  return {
    callbackId,
    unregister: () => {
      if (!telegramCallbacks.delete(callbackId)) return

      callbackRegistrationCount -= 1
      if (
        callbackRegistrationCount === 0 &&
        window.__newApiTelegramWidgetAuth === dispatchTelegramAuthorization
      ) {
        if (previousTelegramCallback) {
          window.__newApiTelegramWidgetAuth = previousTelegramCallback
        } else {
          delete window.__newApiTelegramWidgetAuth
        }
        previousTelegramCallback = undefined
      }
    },
  }
}

function readOptionalString(
  value: Record<string, unknown>,
  key: string,
  maximumLength: number
): string | undefined {
  const field = value[key]
  if (field === undefined || field === null || field === '') return undefined
  if (typeof field !== 'string' || field.length > maximumLength) {
    return undefined
  }
  return field
}

function parseTelegramAuthorization(
  value: unknown
): TelegramAuthPayload | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null

  const record = value as Record<string, unknown>
  if (
    typeof record.id !== 'number' ||
    !Number.isSafeInteger(record.id) ||
    record.id <= 0 ||
    typeof record.auth_date !== 'number' ||
    !Number.isSafeInteger(record.auth_date) ||
    record.auth_date <= 0 ||
    typeof record.hash !== 'string' ||
    !/^[a-f\d]{64}$/i.test(record.hash)
  ) {
    return null
  }

  return {
    id: record.id,
    auth_date: record.auth_date,
    hash: record.hash,
    first_name: readOptionalString(record, 'first_name', 256),
    last_name: readOptionalString(record, 'last_name', 256),
    username: readOptionalString(record, 'username', 64),
    photo_url: readOptionalString(record, 'photo_url', 4096),
    lang: readOptionalString(record, 'lang', 32),
  }
}

function normalizeBotName(botName: string): string | null {
  const normalized = botName.trim().replace(/^@/, '')
  if (!/^[a-z\d_]{5,32}$/i.test(normalized)) return null
  return normalized
}

interface TelegramLoginWidgetProps {
  botName: string
  disabled?: boolean
  loading?: boolean
  loadingLabel?: string
  className?: string
  onAuth: (payload: TelegramAuthPayload) => void | Promise<void>
  onInvalidAuth?: () => void
}

export function TelegramLoginWidget(props: TelegramLoginWidgetProps) {
  const { t } = useTranslation()
  const containerRef = useRef<HTMLDivElement>(null)
  const [scriptLoaded, setScriptLoaded] = useState(false)
  const [scriptFailed, setScriptFailed] = useState(false)
  const normalizedBotName = normalizeBotName(props.botName)
  const unavailable = !normalizedBotName

  const handleAuthorization = useEffectEvent((value: unknown) => {
    const payload = parseTelegramAuthorization(value)
    if (!payload) {
      props.onInvalidAuth?.()
      return
    }
    void props.onAuth(payload)
  })

  useEffect(() => {
    if (props.disabled || props.loading || !normalizedBotName) return

    const container = containerRef.current
    if (!container) return

    setScriptLoaded(false)
    setScriptFailed(false)
    container.replaceChildren()

    const registration = registerTelegramCallback(handleAuthorization)
    const script = document.createElement('script')
    script.src = TELEGRAM_WIDGET_SRC
    script.async = true
    script.referrerPolicy = 'origin'
    script.setAttribute('data-telegram-login', normalizedBotName)
    script.setAttribute('data-size', 'large')
    script.setAttribute('data-userpic', 'false')
    script.setAttribute('data-radius', '8')
    script.setAttribute(
      'data-onauth',
      `window.${TELEGRAM_CALLBACK_NAME}('${registration.callbackId}', user)`
    )
    const handleLoad = () => setScriptLoaded(true)
    const handleError = () => {
      registration.unregister()
      container.replaceChildren()
      setScriptFailed(true)
      setScriptLoaded(false)
    }
    script.addEventListener('load', handleLoad)
    script.addEventListener('error', handleError)
    container.append(script)

    return () => {
      script.removeEventListener('load', handleLoad)
      script.removeEventListener('error', handleError)
      registration.unregister()
      container.replaceChildren()
    }
  }, [normalizedBotName, props.disabled, props.loading])

  if (props.disabled || props.loading) {
    return (
      <Button
        variant='outline'
        type='button'
        disabled
        className={cn(
          'h-11 w-full justify-center gap-2 rounded-lg',
          props.className
        )}
      >
        {props.loading ? (
          <Loader2 aria-hidden='true' className='h-4 w-4 animate-spin' />
        ) : (
          <Send aria-hidden='true' className='h-4 w-4' />
        )}
        {props.loading
          ? (props.loadingLabel ?? t('Loading...'))
          : t('Continue with Telegram')}
      </Button>
    )
  }

  if (unavailable || scriptFailed) {
    return (
      <p
        className={cn('text-destructive text-center text-sm', props.className)}
        role='alert'
      >
        {t('Telegram')}: {t('Loading failed')}
      </p>
    )
  }

  return (
    <div
      className={cn('relative flex min-h-11 justify-center', props.className)}
      aria-label={t('Continue with Telegram')}
      aria-busy={!scriptLoaded}
    >
      {!scriptLoaded ? (
        <span
          className='text-muted-foreground absolute inset-0 flex items-center justify-center gap-2 text-sm'
          role='status'
        >
          <Loader2 aria-hidden='true' className='h-4 w-4 animate-spin' />
          {t('Loading...')}
        </span>
      ) : null}
      <div
        ref={containerRef}
        className={cn(!scriptLoaded && 'invisible')}
        aria-hidden={!scriptLoaded}
      />
    </div>
  )
}
