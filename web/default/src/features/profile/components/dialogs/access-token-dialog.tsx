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
import { RefreshCw, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
  type VerificationMethod,
} from '@/features/auth/secure-verification'

import { useAccessToken } from '../../hooks'

// ============================================================================
// Access Token Dialog Component
// ============================================================================

interface AccessTokenDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function AccessTokenDialog({
  open,
  onOpenChange,
}: AccessTokenDialogProps) {
  const { t } = useTranslation()
  const { token, generating, generate, clear } = useAccessToken()
  const {
    open: verificationOpen,
    setOpen: setVerificationOpen,
    methods: verificationMethods,
    state: verificationState,
    startVerification,
    executeVerification,
    cancel: cancelVerification,
    setCode,
    switchMethod,
  } = useSecureVerification()

  const handleGenerate = async () => {
    await startVerification(generate, {
      title: t('Security verification'),
      description: t(
        'Confirm your identity before accessing this sensitive action.'
      ),
    })
  }

  const handleVerification = async (
    method: VerificationMethod,
    code?: string
  ) => {
    try {
      await executeVerification(method, code)
    } catch {
      // Errors are already shown by the secure-verification hook.
    }
  }

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      clear()
      cancelVerification()
    }
    onOpenChange(nextOpen)
  }

  let generateLabel = t('Generate')
  if (generating) {
    generateLabel = t('Generating...')
  } else if (token) {
    generateLabel = t('Regenerate')
  }

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={handleOpenChange}
        title={t('Access Token')}
        description={t(
          "Your system access token for API authentication. Keep it secure and don't share it with others."
        )}
        contentClassName='sm:max-w-md'
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => handleOpenChange(false)}
            >
              {t('Close')}
            </Button>
            <Button
              type='button'
              onClick={handleGenerate}
              disabled={generating}
              className='gap-2'
            >
              {generating ? (
                <Loader2 className='h-4 w-4 animate-spin' />
              ) : (
                <RefreshCw className='h-4 w-4' />
              )}
              {generateLabel}
            </Button>
          </>
        }
      >
        <div className='my-6 space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor='token'>{t('Token')}</Label>
            <div className='flex gap-2'>
              <Input
                id='token'
                type='text'
                value={token}
                readOnly
                className='font-mono text-xs'
                placeholder={t('Click "Generate" to create a token')}
              />
              <CopyButton
                value={token}
                variant='outline'
                className='size-9'
                iconClassName='size-4'
                tooltip={t('Copy token')}
                aria-label={t('Copy token')}
              />
            </div>
            <p className='text-muted-foreground text-xs'>
              {t('Use this token for API authentication')}
            </p>
          </div>
        </div>
      </Dialog>

      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={(next) =>
          next ? setVerificationOpen(true) : cancelVerification()
        }
        methods={verificationMethods}
        state={verificationState}
        onVerify={handleVerification}
        onCancel={cancelVerification}
        onCodeChange={setCode}
        onMethodChange={switchMethod}
      />
    </>
  )
}
