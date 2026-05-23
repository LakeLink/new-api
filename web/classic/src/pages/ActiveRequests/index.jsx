/*
Copyright (C) 2025 QuantumNous

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

import React, { useState, useEffect, useCallback } from 'react';
import {
  Table,
  Button,
  Tag,
  Switch,
  Typography,
  Popconfirm,
  Space,
  Spin,
} from '@douyinfe/semi-ui';
import { IconRefresh, IconStop, IconWifi } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API } from '../../helpers';

const { Text } = Typography;

function formatElapsed(seconds) {
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return `${m}:${s.toString().padStart(2, '0')}`;
}

function formatChannel(record) {
  const channelName = record.channel_name?.trim();
  if (!channelName) {
    return `#${record.channel_id}`;
  }
  return `${channelName} (${record.channel_id})`;
}

const ActiveRequests = () => {
  const { t } = useTranslation();
  const [requests, setRequests] = useState([]);
  const [completedRetentionSeconds, setCompletedRetentionSeconds] =
    useState(10);
  const [loading, setLoading] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);

  const fetchRequests = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/active-requests');
      if (res.data.success) {
        setRequests(res.data.data || []);
        setCompletedRetentionSeconds(
          res.data.completed_retention_seconds ?? 10,
        );
      }
    } catch (err) {
      // ignore
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchRequests();
  }, [fetchRequests]);

  useEffect(() => {
    if (!autoRefresh) return;
    const interval = setInterval(fetchRequests, 1000);
    return () => clearInterval(interval);
  }, [autoRefresh, fetchRequests]);

  const handleTerminate = async (requestId) => {
    try {
      const res = await API.delete(`/api/active-requests/${requestId}`);
      if (res.data.success) {
        fetchRequests();
      }
    } catch (err) {
      // ignore
    }
  };

  const columns = [
    {
      title: t('Request ID'),
      dataIndex: 'request_id',
      key: 'request_id',
      render: (text) => (
        <Text
          style={{ fontFamily: 'monospace', fontSize: '12px' }}
          ellipsis={{ showTooltip: true }}
        >
          {text?.slice(0, 8)}...
        </Text>
      ),
    },
    {
      title: t('Status'),
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status = 'active') => {
        const isCompleted = status === 'completed';
        return (
          <Tag color={isCompleted ? 'grey' : 'green'} shape='circle'>
            {t(isCompleted ? 'Completed' : 'Active')}
          </Tag>
        );
      },
    },
    {
      title: t('Username'),
      dataIndex: 'user_id',
      key: 'user_id',
      width: 120,
      render: (_, record) =>
        record.username
          ? `${record.username} (#${record.user_id})`
          : record.user_id,
    },
    {
      title: t('Token'),
      dataIndex: 'token_name',
      key: 'token_name',
      render: (text) => text || '-',
    },
    {
      title: t('Model'),
      dataIndex: 'model',
      key: 'model',
      ellipsis: true,
    },
    {
      title: t('Channel'),
      key: 'channel',
      render: (_, record) => formatChannel(record),
    },
    {
      title: t('Elapsed'),
      dataIndex: 'elapsed_seconds',
      key: 'elapsed',
      align: 'right',
      render: (val) => (
        <Text style={{ fontFamily: 'monospace' }}>{formatElapsed(val)}</Text>
      ),
    },
    {
      title: t('Input Tokens'),
      dataIndex: 'input_tokens',
      key: 'input_tokens',
      align: 'right',
    },
    {
      title: t('Output Chunks'),
      dataIndex: 'output_chunks',
      key: 'output_chunks',
      align: 'right',
    },
    {
      title: t('Stale For'),
      dataIndex: 'stale_for_seconds',
      key: 'stale_for',
      align: 'right',
      render: (val, record) => (
        <Text
          style={{
            fontFamily: 'monospace',
            color:
              record.status !== 'completed' && val > 30
                ? 'var(--semi-color-danger)'
                : undefined,
            fontWeight:
              record.status !== 'completed' && val > 30 ? 600 : undefined,
          }}
        >
          {record.status === 'completed'
            ? `${t('Ended')} ${formatElapsed(record.ended_seconds_ago || 0)}`
            : formatElapsed(val)}
        </Text>
      ),
    },
    {
      title: t('Type'),
      dataIndex: 'is_stream',
      key: 'is_stream',
      render: (isStream) => (
        <Tag color={isStream ? 'blue' : 'grey'} shape='circle'>
          {isStream ? (
            <>
              <IconWifi size='extra-small' /> {t('Stream')}
            </>
          ) : (
            t('Normal')
          )}
        </Tag>
      ),
    },
    {
      title: t('IP'),
      dataIndex: 'client_ip',
      key: 'client_ip',
      render: (text) => (
        <Text style={{ fontFamily: 'monospace', fontSize: '12px' }}>
          {text}
        </Text>
      ),
    },
    {
      title: t('Actions'),
      key: 'actions',
      align: 'right',
      render: (_, record) =>
        record.status === 'completed' || record.can_terminate === false ? (
          <Text type='tertiary'>{t('Ended')}</Text>
        ) : (
          <Popconfirm
            title={t('Confirm')}
            content={t('Terminate this request?')}
            onConfirm={() => handleTerminate(record.request_id)}
          >
            <Button type='danger' size='small' icon={<IconStop />}>
              {t('Terminate')}
            </Button>
          </Popconfirm>
        ),
    },
  ];

  const activeCount = requests.filter(
    (request) => (request.status || 'active') === 'active',
  ).length;
  const completedCount = requests.length - activeCount;

  return (
    <div style={{ marginTop: 60, padding: '0 16px' }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <Space>
          <Text heading={4}>{t('Active Requests')}</Text>
          <Text type='tertiary'>
            {t('Total')}: {requests.length}
          </Text>
          <Text type='tertiary'>
            {t('Active')}: {activeCount}
          </Text>
          <Text type='tertiary'>
            {t('Recently ended')}: {completedCount}
          </Text>
          <Text type='tertiary'>
            {t('Ended requests stay visible for')} {completedRetentionSeconds}s
          </Text>
        </Space>
        <Space>
          <Text size='small'>{t('Auto Refresh')} (1s)</Text>
          <Switch checked={autoRefresh} onChange={setAutoRefresh} />
          <Button
            icon={<IconRefresh />}
            onClick={fetchRequests}
            loading={loading}
          >
            {t('Refresh')}
          </Button>
        </Space>
      </div>
      <Table
        columns={columns}
        dataSource={requests}
        rowKey='request_id'
        loading={loading}
        pagination={false}
        size='small'
        empty={
          <Spin spinning={loading}>
            <div style={{ padding: 40, textAlign: 'center' }}>
              {t('No active or recent requests')}
            </div>
          </Spin>
        }
        rowClassName={(record) =>
          record.status !== 'completed' && record.stale_for_seconds > 60
            ? 'stale-row'
            : ''
        }
      />
    </div>
  );
};

export default ActiveRequests;
