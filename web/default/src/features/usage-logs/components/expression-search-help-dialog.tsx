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
import { useQuery } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { CircleAlert } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { useIsAdmin } from '@/hooks/use-admin'
import { useAuthStore } from '@/stores/auth-store'

import { getLogExpressionSchema } from '../api'
import { logExpressionSchemaQueryKey } from '../lib/query-keys'
import type { LogExpressionSchema } from '../types'

interface ExpressionSearchHelpDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  isAdminView: boolean
}

export function ExpressionSearchHelpDialog(
  props: ExpressionSearchHelpDialogProps
) {
  const { t, i18n } = useTranslation()
  const isAdmin = useIsAdmin()
  const currentUserId = useAuthStore((state) => state.auth.user?.id ?? 0)
  const currentUserRole = useAuthStore((state) => state.auth.user?.role ?? 0)
  const schemaQuery = useQuery({
    queryKey: logExpressionSchemaQueryKey(
      currentUserId,
      currentUserRole,
      isAdmin
    ),
    enabled: props.open,
    staleTime: Number.POSITIVE_INFINITY,
    retry: 1,
    queryFn: async ({ signal }): Promise<LogExpressionSchema> => {
      const result = await getLogExpressionSchema(signal)
      const schema = result.data
      if (
        !result.success ||
        !schema ||
        !Array.isArray(schema.fields) ||
        !schema.fields.every((field) => Array.isArray(field.names)) ||
        !Array.isArray(schema.operators) ||
        !Array.isArray(schema.examples) ||
        !schema.limits
      ) {
        throw new Error(result.message || 'Failed to load')
      }
      return schema
    },
  })
  const visibleSchema = schemaQuery.data
    ? {
        ...schemaQuery.data,
        fields: props.isAdminView
          ? schemaQuery.data.fields
          : schemaQuery.data.fields.filter((field) => field.scope !== 'admin'),
        examples: props.isAdminView
          ? schemaQuery.data.examples
          : schemaQuery.data.examples.filter(
              (example) => example.scope !== 'admin'
            ),
      }
    : undefined

  let dialogBody: ReactNode
  if (schemaQuery.isPending) {
    dialogBody = <ExpressionSchemaSkeleton />
  } else if (schemaQuery.isError || !visibleSchema) {
    dialogBody = (
      <Alert variant='destructive'>
        <CircleAlert />
        <AlertDescription className='flex items-center justify-between gap-3'>
          <span>{t('Failed to load')}</span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={schemaQuery.isFetching}
            onClick={() => schemaQuery.refetch()}
          >
            {t('Retry')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  } else {
    dialogBody = (
      <ExpressionSchemaReference
        schema={visibleSchema}
        language={i18n.resolvedLanguage}
      />
    )
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-4xl'>
        <DialogHeader>
          <DialogTitle>{t('Expression Search Reference')}</DialogTitle>
          <DialogDescription>
            {visibleSchema?.intro
              ? translatedSchemaText(
                  t,
                  visibleSchema.intro,
                  visibleSchema.introZh,
                  i18n.resolvedLanguage
                )
              : t(
                  'Expression search is parsed from the AST and translated into SQL with an allowed field list, placeholders, and escaped LIKE patterns.'
                )}
          </DialogDescription>
        </DialogHeader>

        {dialogBody}
      </DialogContent>
    </Dialog>
  )
}

function ExpressionSchemaSkeleton() {
  const { t } = useTranslation()

  return (
    <div
      className='space-y-5'
      role='status'
      aria-busy='true'
      aria-label={t('Loading...')}
    >
      <Skeleton className='h-16 w-full' />
      <Skeleton className='h-64 w-full' />
      <Skeleton className='h-40 w-full' />
    </div>
  )
}

function ExpressionSchemaReference(props: {
  schema: LogExpressionSchema
  language?: string
}) {
  const { t } = useTranslation()

  return (
    <div className='space-y-5'>
      {props.schema.quickSyntax ? (
        <section className='space-y-2'>
          <h3 className='text-sm font-medium'>{t('Quick Syntax')}</h3>
          <p className='text-muted-foreground text-sm'>
            {translatedSchemaText(
              t,
              props.schema.quickSyntax,
              props.schema.quickSyntaxZh,
              props.language
            )}
          </p>
        </section>
      ) : null}

      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>{t('Available Fields')}</h3>
        <div className='overflow-x-auto rounded-md border'>
          <table className='w-full text-left text-sm'>
            <thead className='bg-muted/50 text-muted-foreground'>
              <tr>
                <th scope='col' className='px-3 py-2 font-medium'>
                  {t('Field')}
                </th>
                <th scope='col' className='px-3 py-2 font-medium'>
                  {t('Type')}
                </th>
                <th scope='col' className='px-3 py-2 font-medium'>
                  {t('Availability')}
                </th>
                <th scope='col' className='px-3 py-2 font-medium'>
                  {t('Description')}
                </th>
              </tr>
            </thead>
            <tbody>
              {props.schema.fields.map((field) => (
                <tr key={field.names.join(',')} className='border-t'>
                  <td className='px-3 py-2 align-top'>
                    <code className='bg-muted rounded px-1.5 py-0.5 font-mono text-xs'>
                      {field.names.join(', ')}
                    </code>
                  </td>
                  <td className='px-3 py-2 align-top'>
                    {logExpressionFieldTypeLabel(field.type, t)}
                  </td>
                  <td className='px-3 py-2 align-top'>
                    {field.scope === 'admin'
                      ? t('Admins only')
                      : t('All users')}
                  </td>
                  <td className='text-muted-foreground px-3 py-2 align-top'>
                    {translatedSchemaText(
                      t,
                      field.description,
                      field.descriptionZh,
                      props.language
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>{t('Operators')}</h3>
        <div className='overflow-x-auto rounded-md border'>
          <table className='w-full text-left text-sm'>
            <thead className='bg-muted/50 text-muted-foreground'>
              <tr>
                <th scope='col' className='px-3 py-2 font-medium'>
                  {t('Operator')}
                </th>
                <th scope='col' className='px-3 py-2 font-medium'>
                  {t('Usage')}
                </th>
              </tr>
            </thead>
            <tbody>
              {props.schema.operators.map((operator) => (
                <tr key={operator.syntax} className='border-t'>
                  <td className='px-3 py-2 align-top'>
                    <code className='bg-muted rounded px-1.5 py-0.5 font-mono text-xs'>
                      {operator.syntax}
                    </code>
                  </td>
                  <td className='text-muted-foreground px-3 py-2 align-top'>
                    {translatedSchemaText(
                      t,
                      operator.description,
                      operator.descriptionZh,
                      props.language
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>{t('Useful Expressions')}</h3>
        <div className='grid gap-3 md:grid-cols-2'>
          {props.schema.examples.map((example) => (
            <div key={example.title} className='rounded-md border p-3'>
              <div className='flex items-center justify-between gap-2 font-medium'>
                <span>
                  {translatedSchemaText(
                    t,
                    example.title,
                    example.titleZh,
                    props.language
                  )}
                </span>
                {example.scope === 'admin' ? (
                  <Badge variant='outline'>{t('Admins only')}</Badge>
                ) : null}
              </div>
              <code className='bg-muted mt-2 block break-all rounded px-2 py-1.5 font-mono text-xs'>
                {example.expression}
              </code>
              <p className='text-muted-foreground mt-2 text-sm'>
                {translatedSchemaText(
                  t,
                  example.description,
                  example.descriptionZh,
                  props.language
                )}
              </p>
            </div>
          ))}
        </div>
      </section>

      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>{t('Safety and Limits')}</h3>
        <ul className='text-muted-foreground list-disc space-y-1 ps-5 text-sm'>
          {props.schema.safety ? (
            <li>
              {translatedSchemaText(
                t,
                props.schema.safety,
                props.schema.safetyZh,
                props.language
              )}
            </li>
          ) : null}
          <li>
            <code className='font-mono text-xs'>
              maxLength={props.schema.limits.maxLength}; maxNodes=
              {props.schema.limits.maxNodes}; maxStringLength=
              {props.schema.limits.maxStringLength}; maxInItems=
              {props.schema.limits.maxInItems}
            </code>
          </li>
        </ul>
      </section>
    </div>
  )
}

function translatedSchemaText(
  t: TFunction,
  english: string,
  chinese: string | undefined,
  language: string | undefined
): string {
  const translated = t(english)
  if (translated !== english) return translated
  return language?.startsWith('zh') && chinese ? chinese : translated
}

function logExpressionFieldTypeLabel(type: string, t: TFunction): string {
  switch (type.toLowerCase()) {
    case 'integer':
    case 'number':
    case 'timestamp':
      return t('Number')
    case 'string':
      return t('String')
    case 'boolean':
      return t('Boolean')
    default:
      return type
  }
}
