import { useCallback, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Activity, RefreshCw, StopCircle, Wifi, WifiOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { getActiveRequests, terminateActiveRequest } from './api'
import type { ActiveRequestSnapshot } from './types'

function formatElapsed(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return `${m}:${s.toString().padStart(2, '0')}`
}

export function ActiveRequestsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [autoRefresh, setAutoRefresh] = useState(true)

  const {
    data: activeRequestsResponse,
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

  const requests = activeRequestsResponse?.data ?? []
  const activeCount = requests.filter(
    (request) => (request.status ?? 'active') === 'active'
  ).length
  const completedCount = requests.length - activeCount
  const completedRetentionSeconds =
    activeRequestsResponse?.completed_retention_seconds ?? 10

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
          <CardTitle className='flex flex-wrap items-center gap-x-4 gap-y-1 text-sm font-medium'>
            <span>
              {t('Total')}: {requests.length}
            </span>
            <span>
              {t('Active')}: {activeCount}
            </span>
            <span>
              {t('Recently ended')}: {completedCount}
            </span>
            <span className='text-muted-foreground'>
              {t('Ended requests stay visible for')} {completedRetentionSeconds}
              s
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent className='p-0'>
          {isLoading ? (
            <div className='flex items-center justify-center py-12'>
              <Spinner className='h-8 w-8' />
            </div>
          ) : requests.length === 0 ? (
            <div className='text-muted-foreground flex flex-col items-center justify-center py-12'>
              <Activity className='mb-2 h-8 w-8' />
              <p>{t('No active or recent requests')}</p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Request ID')}</TableHead>
                  <TableHead>{t('Status')}</TableHead>
                  <TableHead>{t('Username')}</TableHead>
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
  const { t } = useTranslation()
  const status = req.status ?? 'active'
  const isCompleted = status === 'completed'
  const isStale = !isCompleted && req.stale_for_seconds > 60
  const canTerminate = req.can_terminate ?? !isCompleted
  const userLabel = req.username
    ? `${req.username} (#${req.user_id})`
    : req.user_id

  return (
    <TableRow
      className={
        isCompleted ? 'opacity-70' : isStale ? 'bg-destructive/5' : undefined
      }
    >
      <TableCell className='font-mono text-xs'>
        {req.request_id.slice(0, 8)}...
      </TableCell>
      <TableCell>
        <Badge variant={isCompleted ? 'outline' : 'secondary'}>
          {t(isCompleted ? 'Completed' : 'Active')}
        </Badge>
      </TableCell>
      <TableCell>{userLabel}</TableCell>
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
            !isCompleted && req.stale_for_seconds > 30
              ? 'text-destructive font-medium'
              : ''
          }
        >
          {isCompleted
            ? `${t('Ended')} ${formatElapsed(req.ended_seconds_ago ?? 0)}`
            : formatElapsed(req.stale_for_seconds)}
        </span>
      </TableCell>
      <TableCell>
        <Badge variant={req.is_stream ? 'default' : 'secondary'}>
          {req.is_stream ? (
            <Wifi className='mr-1 h-3 w-3' />
          ) : (
            <WifiOff className='mr-1 h-3 w-3' />
          )}
          {t(req.is_stream ? 'Stream' : 'Normal')}
        </Badge>
      </TableCell>
      <TableCell className='font-mono text-xs'>{req.client_ip}</TableCell>
      <TableCell className='text-right'>
        {canTerminate ? (
          <Button
            variant='destructive'
            size='sm'
            onClick={() => onTerminate(req.request_id)}
          >
            <StopCircle className='mr-1 h-3 w-3' />
            {t('Terminate')}
          </Button>
        ) : (
          <span className='text-muted-foreground text-sm'>{t('Ended')}</span>
        )}
      </TableCell>
    </TableRow>
  )
}
