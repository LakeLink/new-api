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

import React, { useEffect, useMemo, useState } from 'react';
import { Modal, Spin, Typography } from '@douyinfe/semi-ui';
import { API, renderQuota, showError } from '../../../../helpers';
import {
  calculateBalanceBurnForecast,
  getBalanceBurnForecastRange,
} from '../../../../helpers/dashboard';

const { Text } = Typography;

const formatForecastDate = (date) => {
  if (!date) return '-';
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
};

const getForecastValue = (forecast, t) => {
  if (!forecast) return t('计算中');
  if (forecast.status === 'exhausted') return t('已耗尽');
  if (forecast.status === 'idle') return t('暂无消耗');

  const daysRemaining = forecast.daysRemaining || 0;
  if (daysRemaining < 1) return t('小于 1 天');

  return t('约 {{count}} 天', { count: Math.ceil(daysRemaining) });
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

const normalizeTrend = (values) => {
  const sanitized = (values || []).map((value) =>
    Math.max(0, Number(value) || 0),
  );
  const max = Math.max(...sanitized, 0);
  if (max <= 0) return sanitized.map(() => 0);
  return sanitized.map((value) => Math.max(10, (value / max) * 100));
};

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

  const trend = normalizeTrend(forecast.trend);
  const recentUsage = forecast.dailyBurnQuota * forecast.lookbackDays;

  useEffect(() => {
    const loadForecast = async () => {
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
    };

    loadForecast();
  }, [visible, user?.username]);

  return (
    <Modal
      visible={visible}
      onCancel={onCancel}
      title={t('余额燃尽预测')}
      footer={null}
      width={560}
    >
      <Spin spinning={loading} tip={t('计算中')}>
        <div className='space-y-4'>
          <div className='rounded-xl border border-amber-100 bg-amber-50 p-4'>
            <div className='text-xs text-gray-500'>
              {user?.username || '-'} (ID: {user?.id || '-'})
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
            <div className='mt-3 flex h-12 items-end gap-1'>
              {trend.map((height, index) => (
                <span
                  key={`${user?.id || 'user'}-burn-${index}`}
                  className='flex-1 rounded-t-sm bg-amber-400'
                  style={{ height: `${height}%` }}
                />
              ))}
            </div>
          </div>
        </div>
      </Spin>
    </Modal>
  );
};

export default UserBurnForecastModal;
