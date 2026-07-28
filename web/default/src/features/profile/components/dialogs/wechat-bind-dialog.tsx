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
import { isAxiosError } from 'axios'
import { Loader2, QrCode } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { bindWeChat } from '../../api'

// ============================================================================
// WeChat Bind Dialog Component
// ============================================================================

interface WeChatBindDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
  qrCodeUrl?: string
}

export function WeChatBindDialog(props: WeChatBindDialogProps) {
  const { t } = useTranslation()
  const [code, setCode] = useState('')
  const [binding, setBinding] = useState(false)
  const bindingRef = useRef(false)

  const handleOpenChange = (open: boolean) => {
    if (bindingRef.current) return

    props.onOpenChange(open)
    if (!open) {
      setCode('')
    }
  }

  const handleBind = async () => {
    if (bindingRef.current) return

    const verificationCode = code.trim()
    if (!verificationCode) {
      toast.error(t('Please enter the verification code'))
      return
    }

    bindingRef.current = true
    setBinding(true)
    try {
      const response = await bindWeChat(verificationCode)
      if (!response.success) {
        toast.error(response.message || t('Request failed'))
        return
      }

      toast.success(t('Binding successful!'))
      setCode('')
      props.onOpenChange(false)
      props.onSuccess()
    } catch (error: unknown) {
      const message = isAxiosError<{ message?: unknown }>(error)
        ? error.response?.data?.message
        : undefined
      toast.error(
        typeof message === 'string' && message ? message : t('Request failed')
      )
    } finally {
      bindingRef.current = false
      setBinding(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={handleOpenChange}
      title={t('Bind WeChat Account')}
      description={t('Scan the QR code with WeChat to bind your account')}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => handleOpenChange(false)}
            disabled={binding}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            onClick={handleBind}
            disabled={binding || !code.trim()}
          >
            {binding ? (
              <Loader2
                aria-hidden='true'
                className='mr-2 h-4 w-4 animate-spin'
              />
            ) : null}
            {binding ? t('Binding...') : t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-4 py-4'>
        <Alert>
          <QrCode aria-hidden='true' className='h-4 w-4' />
          <AlertDescription>
            {t(
              'Scan the QR code to follow the official account and reply with “验证码” to receive your verification code.'
            )}
          </AlertDescription>
        </Alert>

        {props.qrCodeUrl ? (
          <div className='flex justify-center'>
            <img
              src={props.qrCodeUrl}
              alt={t('WeChat login QR code')}
              className='h-48 w-48 rounded-md border object-contain'
            />
          </div>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('QR code is not configured. Please contact support.')}
          </p>
        )}

        <div className='grid gap-2'>
          <Label htmlFor='wechat-bind-code'>{t('Verification code')}</Label>
          <Input
            id='wechat-bind-code'
            value={code}
            onChange={(event) => setCode(event.target.value)}
            placeholder={t('Enter the verification code')}
            autoComplete='one-time-code'
            disabled={binding}
          />
        </div>
      </div>
    </Dialog>
  )
}
