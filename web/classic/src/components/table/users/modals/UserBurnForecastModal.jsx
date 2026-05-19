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

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Modal, Spin, Typography } from '@douyinfe/semi-ui';
import { IconRefresh } from '@douyinfe/semi-icons';
import { VChart } from '@visactor/react-vchart';
import { API, renderQuota, showError } from '../../../../helpers';
import {
  calculateBalanceBurnForecast,
  formatBurnDurationPrecise,
  getBalanceBurnForecastRange,
} from '../../../../helpers/dashboard';

const { Text } = Typography;

const formatForecastDate = (date) => {
  if (!date) return '-';
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  const hours = String(date.getHours()).padStart(2, '0');
  const minutes = String(date.getMinutes()).padStart(2, '0');
  const seconds = String(date.getSeconds()).padStart(2, '0');
  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`;
};

const getForecastValue = (forecast, t) => {
  if (!forecast) return t('计算中');
  return formatBurnDurationPrecise(forecast, t);
};

const getForecastDetail = (forecast, t) => {
  if (!forecast) return t('正在加载近期用量');
  if (forecast.status === 'exhausted') return t('余额已经耗尽');
  if (forecast.status === 'idle') {
    return t('近 {{count}} 天无额度消耗', {
      count: Math.round(forecast.lookbackDays || 7),
    });
  }

  return t('预计 {{date}} 用尽', {
    date: formatForecastDate(forecast.estimatedEmptyAt),
  });
};

const getTrendChartSpec = (trend, t) => ({
  type: 'bar',
  height: 150,
  padding: { top: 8, right: 8, bottom: 4, left: 8 },
  data: [
    {
      id: 'burn-trend',
      values: (trend || []).map((value, index) => ({
        bucket: t('第 {{count}} 天', { count: index + 1 }),
        quota: Number(value) || 0,
      })),
    },
  ],
  xField: 'bucket',
  yField: 'quota',
  bar: {
    style: {
      fill: '#f59e0b',
      cornerRadius: [4, 4, 0, 0],
    },
  },
  axes: [
    {
      orient: 'bottom',
      label: { visible: false },
      tick: { visible: false },
      domainLine: { visible: false },
    },
    {
      orient: 'left',
      label: { visible: false },
      tick: { visible: false },
      domainLine: { visible: false },
      grid: { visible: false },
    },
  ],
  tooltip: {
    mark: {
      title: { value: (datum) => datum?.bucket || '' },
      content: [
        {
          key: t('消耗'),
          value: (datum) => renderQuota(Number(datum?.quota || 0)),
        },
      ],
    },
  },
});

const ForecastMetric = ({ label, value }) => (
  <div className='rounded-lg border border-gray-100 bg-white p-3'>
    <div className='text-xs text-gray-500'>{label}</div>
    <div className='mt-1 break-all font-mono text-sm font-semibold'>
      {value}
    </div>
  </div>
);

const UserBurnForecastModal = ({ visible, onCancel, user, t }) => {
  const [loading, setLoading] = useState(false);
  const [range, setRange] = useState(() => getBalanceBurnForecastRange());
  const [quotaData, setQuotaData] = useState([]);

  const forecast = useMemo(
    () =>
      calculateBalanceBurnForecast(
        quotaData,
        user?.quota,
        range.start,
        range.end,
      ),
    [quotaData, range.end, range.start, user?.quota],
  );

  const recentUsage = forecast.recentUsageQuota;
  const hasTrend = forecast.trend.some((value) => Number(value) > 0);

  const loadForecast = useCallback(async () => {
    if (!visible || !user?.username) return;

    const nextRange = getBalanceBurnForecastRange();
    setRange(nextRange);
    setLoading(true);
    try {
      const res = await API.get(
        `/api/data/?username=${encodeURIComponent(user.username)}&start_timestamp=${nextRange.start}&end_timestamp=${nextRange.end}&default_time=hour`,
      );
      const { success, message, data } = res.data;
      if (success) {
        setQuotaData(data || []);
      } else {
        showError(message);
        setQuotaData([]);
      }
    } catch (error) {
      showError(error.message);
      setQuotaData([]);
    } finally {
      setLoading(false);
    }
  }, [visible, user?.username]);

  useEffect(() => {
    loadForecast();
  }, [loadForecast]);

  return (
    <Modal
      visible={visible}
      onCancel={onCancel}
      title={
        <div className='flex items-center justify-between gap-3 pr-8'>
          <span>{t('余额燃尽预测')}</span>
          <Button
            size='small'
            icon={<IconRefresh />}
            loading={loading}
            onClick={(event) => {
              event.stopPropagation();
              loadForecast();
            }}
          >
            {t('刷新')}
          </Button>
        </div>
      }
      footer={null}
      width={560}
    >
      <Spin spinning={loading} tip={t('计算中')}>
        <div className='space-y-4'>
          <div className='rounded-xl border border-amber-100 bg-amber-50 p-4'>
            <div className='text-xs text-gray-500'>
              {t('{{username}}（ID：{{id}}）', {
                username: user?.username || '-',
                id: user?.id || '-',
              })}
            </div>
            <div className='mt-2 font-mono text-2xl font-semibold'>
              {getForecastValue(forecast, t)}
            </div>
            <Text type='secondary' size='small'>
              {getForecastDetail(forecast, t)}
            </Text>
          </div>

          <div className='grid grid-cols-1 gap-2 md:grid-cols-3'>
            <ForecastMetric
              label={t('当前余额')}
              value={renderQuota(Number(user?.quota || 0))}
            />
            <ForecastMetric
              label={t('历史消耗')}
              value={renderQuota(Number(user?.used_quota || 0))}
            />
            <ForecastMetric
              label={t('近期消耗')}
              value={renderQuota(recentUsage)}
            />
          </div>

          <div className='rounded-lg border border-gray-100 bg-white p-3'>
            <div className='text-xs text-gray-500'>
              {t('日均消耗 {{value}}', {
                value: renderQuota(forecast.dailyBurnQuota),
              })}
            </div>
            <div className='mt-3 h-[150px]'>
              {hasTrend ? (
                <VChart
                  spec={getTrendChartSpec(forecast.trend, t)}
                  option={{ mode: 'desktop-browser' }}
                />
              ) : (
                <div className='flex h-full items-center justify-center text-xs text-gray-400'>
                  {t('暂无消耗')}
                </div>
              )}
            </div>
          </div>
        </div>
      </Spin>
    </Modal>
  );
};

export default UserBurnForecastModal;
