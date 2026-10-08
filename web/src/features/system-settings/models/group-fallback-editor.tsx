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
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'

type FallbackRule = {
  fallback: string[]
  pricing_mode: 'origin' | 'target'
  origin_pricing_use_special_ratio?: boolean
  target_pricing_ratio_mode?: string
}

const ratioOptions = [
  ['origin_special', 'Use source special ratio only'],
  ['target_special', 'Use target special ratio only'],
  ['normal_only', 'Use normal target ratio only'],
  ['prefer_origin_special', 'Prefer source special ratio'],
  ['prefer_target_special', 'Prefer target special ratio'],
] as const

export function GroupFallbackEditor({
  value,
  onChange,
}: {
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const rules = useMemo(() => {
    try {
      const parsed: unknown = JSON.parse(value || '{}')
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        return null
      }
      for (const rule of Object.values(parsed)) {
        if (
          !rule ||
          !Array.isArray(rule.fallback) ||
          !rule.fallback.every((group: unknown) => typeof group === 'string')
        ) {
          return null
        }
      }
      return parsed as Record<string, FallbackRule>
    } catch {
      return null
    }
  }, [value])
  const [open, setOpen] = useState(false)
  const [editKey, setEditKey] = useState<string | null>(null)
  const [source, setSource] = useState('')
  const [chain, setChain] = useState('')
  const [draft, setDraft] = useState<FallbackRule>({
    fallback: [],
    pricing_mode: 'target',
  })
  const [deleteKey, setDeleteKey] = useState<string | null>(null)
  const sourceKey = source.trim()
  const fallback = chain
    .split(',')
    .map((group) => group.trim())
    .filter(Boolean)
  const canSave =
    !!rules &&
    !!sourceKey &&
    fallback.length > 0 &&
    (editKey === sourceKey || !Object.hasOwn(rules, sourceKey))

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Group fallback')}</CardTitle>
        <CardDescription>
          {t(
            'When a group has no available channel, the system tries fallback groups in order. Pricing mode determines which group ratio is used for billing.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        {rules ? (
          <>
            <Button
              type='button'
              size='sm'
              onClick={() => {
                setEditKey(null)
                setSource('')
                setChain('')
                setDraft({
                  fallback: [],
                  pricing_mode: 'target',
                  origin_pricing_use_special_ratio: true,
                  target_pricing_ratio_mode: 'target_special',
                })
                setOpen(true)
              }}
            >
              {t('Add fallback rule')}
            </Button>
            {Object.entries(rules).map(([group, rule]) => {
              let ratioLabel: string =
                ratioOptions.find(
                  ([mode]) => mode === rule.target_pricing_ratio_mode
                )?.[1] ?? 'Use target special ratio only'
              if (rule.pricing_mode === 'origin') {
                ratioLabel =
                  rule.origin_pricing_use_special_ratio === false
                    ? 'Normal source ratio only'
                    : 'Use source special ratio'
              }
              return (
                <div
                  key={group}
                  className='flex flex-wrap items-center gap-3 rounded-lg border p-3'
                >
                  <div className='min-w-0 flex-1 space-y-1'>
                    <p className='font-medium break-words'>
                      {group} → {rule.fallback.join(' → ')}
                    </p>
                    <p className='text-muted-foreground text-xs'>
                      {rule.pricing_mode === 'origin'
                        ? t('Origin pricing')
                        : t('Target pricing')}{' '}
                      · {t(ratioLabel)}
                    </p>
                  </div>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    aria-label={`${t('Edit fallback rule')}: ${group}`}
                    onClick={() => {
                      setEditKey(group)
                      setSource(group)
                      setChain(rule.fallback.join(', '))
                      setDraft({ ...rule })
                      setOpen(true)
                    }}
                  >
                    {t('Edit')}
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    aria-label={`${t('Delete')}: ${group}`}
                    onClick={() => setDeleteKey(group)}
                  >
                    {t('Delete')}
                  </Button>
                </div>
              )
            })}
          </>
        ) : (
          <JsonCodeEditor value={value} onChange={onChange} />
        )}
      </CardContent>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={editKey ? t('Edit fallback rule') : t('Add fallback rule')}
        description={t(
          'Configure which groups to fall back to when the source group has no available channel.'
        )}
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              disabled={!canSave}
              onClick={() => {
                if (!canSave || !rules) return
                onChange(
                  JSON.stringify(
                    { ...rules, [sourceKey]: { ...draft, fallback } },
                    null,
                    2
                  )
                )
                setOpen(false)
              }}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor={`${id}-source`}>{t('Source group')}</Label>
            <Input
              id={`${id}-source`}
              value={source}
              disabled={editKey !== null}
              onChange={(event) => setSource(event.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor={`${id}-chain`}>
              {t('Fallback groups (comma-separated, in order)')}
            </Label>
            <Input
              id={`${id}-chain`}
              value={chain}
              onChange={(event) => setChain(event.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor={`${id}-pricing`}>{t('Pricing mode')}</Label>
            <NativeSelect
              id={`${id}-pricing`}
              value={draft.pricing_mode}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  pricing_mode: event.target
                    .value as FallbackRule['pricing_mode'],
                })
              }
            >
              <NativeSelectOption value='origin'>
                {t('Bill at source group rate')}
              </NativeSelectOption>
              <NativeSelectOption value='target'>
                {t('Bill at serving group rate')}
              </NativeSelectOption>
            </NativeSelect>
          </div>
          {draft.pricing_mode === 'origin' ? (
            <div className='flex items-center justify-between gap-4'>
              <Label htmlFor={`${id}-special`}>
                {t('Use special ratio for origin pricing')}
              </Label>
              <Switch
                id={`${id}-special`}
                checked={draft.origin_pricing_use_special_ratio !== false}
                onCheckedChange={(checked) =>
                  setDraft({
                    ...draft,
                    origin_pricing_use_special_ratio: checked,
                  })
                }
              />
            </div>
          ) : (
            <div className='space-y-2'>
              <Label htmlFor={`${id}-ratio`}>
                {t('Special ratio handling')}
              </Label>
              <NativeSelect
                id={`${id}-ratio`}
                value={draft.target_pricing_ratio_mode ?? 'target_special'}
                onChange={(event) =>
                  setDraft({
                    ...draft,
                    target_pricing_ratio_mode: event.target.value,
                  })
                }
              >
                {ratioOptions.map(([mode, label]) => (
                  <NativeSelectOption key={mode} value={mode}>
                    {t(label)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          )}
        </div>
      </Dialog>
      <ConfirmDialog
        open={deleteKey !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setDeleteKey(null)
        }}
        title={t('Delete')}
        desc={deleteKey ?? ''}
        destructive
        confirmText={t('Delete')}
        handleConfirm={() => {
          if (deleteKey === null || !rules) return
          const next = { ...rules }
          delete next[deleteKey]
          onChange(JSON.stringify(next, null, 2))
          setDeleteKey(null)
        }}
      />
    </Card>
  )
}
