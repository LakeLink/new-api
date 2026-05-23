import { useCallback, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  Activity,
  RefreshCw,
  StopCircle,
  Wifi,
  WifiOff,
} from 'lucide-react'

import { getActiveRequests, terminateActiveRequest } from './api'
import type { ActiveRequestSnapshot } from './types'

import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Spinner } from '@/components/ui/spinner'
import { toast } from 'sonner'

function formatElapsed(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return `${m}:${s.toString().padStart(2, '0')}`
}

export function ActiveRequestsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [autoRefresh, setAutoRefresh] = useState(false)

  const {
    data: requests,
    isLoading,
    isFetching,
  } = useQuery({
    queryKey: ['active-requests'],
    queryFn: getActiveRequests,
    refetchInterval: autoRefresh ? 1000 : false,
  })

  const terminateMutation = useMutation({
    mutationFn: terminateActiveRequest,
    onSuccess: () => {
      toast.success(t('Request terminated'))
      queryClient.invalidateQueries({ queryKey: ['active-requests'] })
    },
    onError: () => {
      toast.error(t('Failed to terminate request'))
    },
  })

  const handleRefresh = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ['active-requests'] })
  }, [queryClient])

  return (
    <div className='flex-1 space-y-4 p-4 pt-6 md:p-8'>
      <div className='flex items-center justify-between'>
        <div className='flex items-center gap-2'>
          <Activity className='h-6 w-6' />
          <h2 className='text-2xl font-bold tracking-tight'>
            {t('Active Requests')}
          </h2>
          {isFetching && <Spinner className='h-4 w-4' />}
        </div>
        <div className='flex items-center gap-4'>
          <div className='flex items-center gap-2'>
            <Switch
              id='auto-refresh'
              checked={autoRefresh}
              onCheckedChange={setAutoRefresh}
            />
            <Label htmlFor='auto-refresh' className='text-sm'>
              {t('Auto Refresh')} (1s)
            </Label>
          </div>
          <Button
            variant='outline'
            size='sm'
            onClick={handleRefresh}
            disabled={isFetching}
          >
            <RefreshCw className='mr-2 h-4 w-4' />
            {t('Refresh')}
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader className='py-3'>
          <CardTitle className='text-sm font-medium'>
            {t('Total')}: {requests?.length ?? 0}
          </CardTitle>
        </CardHeader>
        <CardContent className='p-0'>
          {isLoading ? (
            <div className='flex items-center justify-center py-12'>
              <Spinner className='h-8 w-8' />
            </div>
          ) : !requests || requests.length === 0 ? (
            <div className='flex flex-col items-center justify-center py-12 text-muted-foreground'>
              <Activity className='mb-2 h-8 w-8' />
              <p>{t('No active requests')}</p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Request ID')}</TableHead>
                  <TableHead>{t('User')}</TableHead>
                  <TableHead>{t('Token')}</TableHead>
                  <TableHead>{t('Model')}</TableHead>
                  <TableHead>{t('Channel')}</TableHead>
                  <TableHead className='text-right'>{t('Elapsed')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Input Tokens')}
                  </TableHead>
                  <TableHead className='text-right'>
                    {t('Output Chunks')}
                  </TableHead>
                  <TableHead className='text-right'>{t('Stale For')}</TableHead>
                  <TableHead>{t('Type')}</TableHead>
                  <TableHead>{t('IP')}</TableHead>
                  <TableHead className='text-right'>{t('Actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {requests.map((req) => (
                  <ActiveRequestRow
                    key={req.request_id}
                    req={req}
                    onTerminate={(id) => terminateMutation.mutate(id)}
                  />
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function ActiveRequestRow({
  req,
  onTerminate,
}: {
  req: ActiveRequestSnapshot
  onTerminate: (id: string) => void
}) {
  const isStale = req.stale_for_seconds > 60
  const { t } = useTranslation()

  return (
    <TableRow className={isStale ? 'bg-destructive/5' : undefined}>
      <TableCell className='font-mono text-xs'>
        {req.request_id.slice(0, 8)}...
      </TableCell>
      <TableCell>{req.user_id}</TableCell>
      <TableCell>{req.token_name || '-'}</TableCell>
      <TableCell className='max-w-[200px] truncate'>{req.model}</TableCell>
      <TableCell>
        {req.channel_id} / {req.channel_type}
      </TableCell>
      <TableCell className='text-right font-mono'>
        {formatElapsed(req.elapsed_seconds)}
      </TableCell>
      <TableCell className='text-right'>{req.input_tokens}</TableCell>
      <TableCell className='text-right'>{req.output_chunks}</TableCell>
      <TableCell className='text-right'>
        <span
          className={
            req.stale_for_seconds > 30 ? 'text-destructive font-medium' : ''
          }
        >
          {formatElapsed(req.stale_for_seconds)}
        </span>
      </TableCell>
      <TableCell>
        <Badge variant={req.is_stream ? 'default' : 'secondary'}>
          {req.is_stream ? (
            <Wifi className='mr-1 h-3 w-3' />
          ) : (
            <WifiOff className='mr-1 h-3 w-3' />
          )}
          {req.is_stream ? 'Stream' : 'Normal'}
        </Badge>
      </TableCell>
      <TableCell className='font-mono text-xs'>{req.client_ip}</TableCell>
      <TableCell className='text-right'>
        <Button
          variant='destructive'
          size='sm'
          onClick={() => onTerminate(req.request_id)}
        >
          <StopCircle className='mr-1 h-3 w-3' />
          {t('Terminate')}
        </Button>
      </TableCell>
    </TableRow>
  )
}
