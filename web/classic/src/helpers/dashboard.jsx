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

import React from 'react';
import { Progress, Divider, Empty } from '@douyinfe/semi-ui';
import {
  IllustrationConstruction,
  IllustrationConstructionDark,
} from '@douyinfe/semi-illustrations';
import {
  timestamp2string,
  timestamp2string1,
  isDataCrossYear,
  copy,
  showSuccess,
} from './utils';
import {
  STORAGE_KEYS,
  DEFAULT_TIME_INTERVALS,
  DEFAULTS,
  ILLUSTRATION_SIZE,
} from '../constants/dashboard.constants';

const SECONDS_PER_DAY = 24 * 60 * 60;

export const BALANCE_BURN_FORECAST_DAYS = 7;

// ========== 时间相关工具函数 ==========
export const getDefaultTime = () => {
  return localStorage.getItem(STORAGE_KEYS.DATA_EXPORT_DEFAULT_TIME) || 'hour';
};

export const getTimeInterval = (timeType, isSeconds = false) => {
  const intervals =
    DEFAULT_TIME_INTERVALS[timeType] || DEFAULT_TIME_INTERVALS.hour;
  return isSeconds ? intervals.seconds : intervals.minutes;
};

export const getInitialTimestamp = () => {
  const defaultTime = getDefaultTime();
  const now = new Date().getTime() / 1000;

  switch (defaultTime) {
    case 'hour':
      return timestamp2string(now - 86400);
    case 'week':
      return timestamp2string(now - 86400 * 30);
    default:
      return timestamp2string(now - 86400 * 7);
  }
};

// ========== 数据处理工具函数 ==========
export const updateMapValue = (map, key, value) => {
  if (!map.has(key)) {
    map.set(key, 0);
  }
  map.set(key, map.get(key) + value);
};

export const initializeMaps = (key, ...maps) => {
  maps.forEach((map) => {
    if (!map.has(key)) {
      map.set(key, 0);
    }
  });
};

// ========== 图表相关工具函数 ==========
export const updateChartSpec = (
  setterFunc,
  newData,
  subtitle,
  newColors,
  dataId,
) => {
  setterFunc((prev) => ({
    ...prev,
    data: [{ id: dataId, values: newData }],
    title: {
      ...prev.title,
      subtext: subtitle,
    },
    color: {
      specified: newColors,
    },
  }));
};

export const getTrendSpec = (data, color) => ({
  type: 'line',
  data: [{ id: 'trend', values: data.map((val, idx) => ({ x: idx, y: val })) }],
  xField: 'x',
  yField: 'y',
  height: 40,
  width: 100,
  axes: [
    {
      orient: 'bottom',
      visible: false,
    },
    {
      orient: 'left',
      visible: false,
    },
  ],
  padding: 0,
  autoFit: false,
  legends: { visible: false },
  tooltip: { visible: false },
  crosshair: { visible: false },
  line: {
    style: {
      stroke: color,
      lineWidth: 2,
    },
  },
  point: {
    visible: false,
  },
  background: {
    fill: 'transparent',
  },
});

// ========== UI 工具函数 ==========
export const createSectionTitle = (Icon, text) => (
  <div className='flex items-center gap-2'>
    <Icon size={16} />
    {text}
  </div>
);

export const createFormField = (Component, props, FORM_FIELD_PROPS) => (
  <Component {...FORM_FIELD_PROPS} {...props} />
);

// ========== 操作处理函数 ==========
export const handleCopyUrl = async (url, t) => {
  if (await copy(url)) {
    showSuccess(t('复制成功'));
  }
};

export const handleSpeedTest = (apiUrl) => {
  const encodedUrl = encodeURIComponent(apiUrl);
  const speedTestUrl = `https://www.tcptest.cn/http/${encodedUrl}`;
  window.open(speedTestUrl, '_blank', 'noopener,noreferrer');
};

// ========== 状态映射函数 ==========
export const getUptimeStatusColor = (status, uptimeStatusMap) =>
  uptimeStatusMap[status]?.color || '#8b9aa7';

export const getUptimeStatusText = (status, uptimeStatusMap, t) =>
  uptimeStatusMap[status]?.text || t('未知');

// ========== 监控列表渲染函数 ==========
export const renderMonitorList = (
  monitors,
  getUptimeStatusColor,
  getUptimeStatusText,
  t,
) => {
  if (!monitors || monitors.length === 0) {
    return (
      <div className='flex justify-center items-center py-4'>
        <Empty
          image={<IllustrationConstruction style={ILLUSTRATION_SIZE} />}
          darkModeImage={
            <IllustrationConstructionDark style={ILLUSTRATION_SIZE} />
          }
          title={t('暂无监控数据')}
        />
      </div>
    );
  }

  const grouped = {};
  monitors.forEach((m) => {
    const g = m.group || '';
    if (!grouped[g]) grouped[g] = [];
    grouped[g].push(m);
  });

  const renderItem = (monitor, idx) => (
    <div key={idx} className='p-2 hover:bg-white rounded-lg transition-colors'>
      <div className='flex items-center justify-between mb-1'>
        <div className='flex items-center gap-2'>
          <div
            className='w-2 h-2 rounded-full flex-shrink-0'
            style={{ backgroundColor: getUptimeStatusColor(monitor.status) }}
          />
          <span className='text-sm font-medium text-gray-900'>
            {monitor.name}
          </span>
        </div>
        <span className='text-xs text-gray-500'>
          {((monitor.uptime || 0) * 100).toFixed(2)}%
        </span>
      </div>
      <div className='flex items-center gap-2'>
        <span className='text-xs text-gray-500'>
          {getUptimeStatusText(monitor.status)}
        </span>
        <div className='flex-1'>
          <Progress
            percent={(monitor.uptime || 0) * 100}
            showInfo={false}
            aria-label={`${monitor.name} uptime`}
            stroke={getUptimeStatusColor(monitor.status)}
          />
        </div>
      </div>
    </div>
  );

  return Object.entries(grouped).map(([gname, list]) => (
    <div key={gname || 'default'} className='mb-2'>
      {gname && (
        <>
          <div className='text-md font-semibold text-gray-500 px-2 py-1'>
            {gname}
          </div>
          <Divider />
        </>
      )}
      {list.map(renderItem)}
    </div>
  ));
};

// ========== 数据处理函数 ==========
export const processRawData = (
  data,
  dataExportDefaultTime,
  initializeMaps,
  updateMapValue,
) => {
  const result = {
    totalQuota: 0,
    totalTimes: 0,
    totalTokens: 0,
    uniqueModels: new Set(),
    timePoints: [],
    timeQuotaMap: new Map(),
    timeTokensMap: new Map(),
    timeCountMap: new Map(),
  };

  // 检查数据是否跨年
  const showYear = isDataCrossYear(data.map((item) => item.created_at));

  data.forEach((item) => {
    result.uniqueModels.add(item.model_name);
    result.totalTokens += item.token_used;
    result.totalQuota += item.quota;
    result.totalTimes += item.count;

    const timeKey = timestamp2string1(
      item.created_at,
      dataExportDefaultTime,
      showYear,
    );
    if (!result.timePoints.includes(timeKey)) {
      result.timePoints.push(timeKey);
    }

    initializeMaps(
      timeKey,
      result.timeQuotaMap,
      result.timeTokensMap,
      result.timeCountMap,
    );
    updateMapValue(result.timeQuotaMap, timeKey, item.quota);
    updateMapValue(result.timeTokensMap, timeKey, item.token_used);
    updateMapValue(result.timeCountMap, timeKey, item.count);
  });

  result.timePoints.sort();
  return result;
};

export const calculateTrendData = (
  timePoints,
  timeQuotaMap,
  timeTokensMap,
  timeCountMap,
  dataExportDefaultTime,
) => {
  const quotaTrend = timePoints.map((time) => timeQuotaMap.get(time) || 0);
  const tokensTrend = timePoints.map((time) => timeTokensMap.get(time) || 0);
  const countTrend = timePoints.map((time) => timeCountMap.get(time) || 0);

  const rpmTrend = [];
  const tpmTrend = [];

  if (timePoints.length >= 2) {
    const interval = getTimeInterval(dataExportDefaultTime);

    for (let i = 0; i < timePoints.length; i++) {
      rpmTrend.push(timeCountMap.get(timePoints[i]) / interval);
      tpmTrend.push(timeTokensMap.get(timePoints[i]) / interval);
    }
  }

  return {
    balance: [],
    usedQuota: [],
    requestCount: [],
    times: countTrend,
    consumeQuota: quotaTrend,
    tokens: tokensTrend,
    rpm: rpmTrend,
    tpm: tpmTrend,
  };
};

const getBucketIndex = (timestamp, start, end, bucketCount) => {
  if (end <= start) return 0;
  const ratio = (timestamp - start) / (end - start);
  return Math.min(
    bucketCount - 1,
    Math.max(0, Math.floor(ratio * bucketCount)),
  );
};

const SECONDS_PER_HOUR = 60 * 60;
const SECONDS_PER_MINUTE = 60;

export const getBalanceBurnForecastRange = () => {
  const end = Math.floor(Date.now() / 1000) + 3600;
  const start = end - BALANCE_BURN_FORECAST_DAYS * SECONDS_PER_DAY;
  return { start, end };
};

const getPositiveQuota = (item) => Math.max(0, Number(item.quota) || 0);

const calculateWindowUsage = (data, windowStart, effectiveEnd) =>
  (data || []).reduce((total, item) => {
    const timestamp = Number(item.created_at) || 0;
    if (timestamp < windowStart || timestamp > effectiveEnd) return total;
    return total + getPositiveQuota(item);
  }, 0);

const calculateHourlyBurnRate = (
  totalUsage,
  recent24Usage,
  recent48Usage,
  lookbackSeconds,
) => {
  const lookbackHours = Math.max(lookbackSeconds / SECONDS_PER_HOUR, 1);
  const fullRate = totalUsage / lookbackHours;
  const recent24Rate = recent24Usage / Math.min(24, lookbackHours);
  const recent48Rate = recent48Usage / Math.min(48, lookbackHours);

  if (recent24Usage > 0) {
    return recent24Rate * 0.55 + recent48Rate * 0.3 + fullRate * 0.15;
  }

  if (recent48Usage > 0) {
    return recent48Rate * 0.65 + fullRate * 0.35;
  }

  return fullRate;
};

export const getDurationParts = (totalSeconds) => {
  const normalized = Math.max(0, Math.floor(Number(totalSeconds) || 0));
  const days = Math.floor(normalized / SECONDS_PER_DAY);
  const hours = Math.floor((normalized % SECONDS_PER_DAY) / SECONDS_PER_HOUR);
  const minutes = Math.floor(
    (normalized % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE,
  );
  const seconds = normalized % SECONDS_PER_MINUTE;

  return { days, hours, minutes, seconds };
};

export const formatBurnDurationCompact = (forecast, t) => {
  if (forecast.status === 'exhausted') return t('已耗尽');
  if (forecast.status === 'idle' || forecast.secondsRemaining === null) {
    return t('暂无消耗');
  }

  if (forecast.secondsRemaining < SECONDS_PER_MINUTE) {
    return t('小于 1 分钟');
  }

  if (forecast.secondsRemaining < 2 * SECONDS_PER_DAY) {
    const totalMinutes = Math.max(
      1,
      Math.ceil(forecast.secondsRemaining / SECONDS_PER_MINUTE),
    );
    const hours = Math.floor(totalMinutes / 60);
    const minutes = totalMinutes % 60;
    return t('{{hours}}小时{{minutes}}分钟', { hours, minutes });
  }

  return t('{{count}}天', {
    count: Math.ceil(forecast.secondsRemaining / SECONDS_PER_DAY),
  });
};

export const formatBurnDurationPrecise = (forecast, t) => {
  if (forecast.status === 'idle' || forecast.secondsRemaining === null) {
    return t('暂无消耗');
  }

  const parts = getDurationParts(forecast.secondsRemaining);
  return t('{{days}}天{{hours}}小时{{minutes}}分钟{{seconds}}秒', parts);
};

export const calculateBalanceBurnForecast = (
  data,
  currentBalance,
  start,
  end,
  bucketCount = BALANCE_BURN_FORECAST_DAYS,
) => {
  const nowSeconds = Math.floor(Date.now() / 1000);
  const effectiveEnd = Math.max(
    start + SECONDS_PER_HOUR,
    Math.min(end, nowSeconds),
  );
  const lookbackSeconds = Math.max(effectiveEnd - start, SECONDS_PER_HOUR);
  const lookbackDays = lookbackSeconds / SECONDS_PER_DAY;
  const trend = Array.from({ length: bucketCount }, () => 0);
  let totalUsage = 0;

  (data || []).forEach((item) => {
    const timestamp = Number(item.created_at) || start;
    if (timestamp < start || timestamp > effectiveEnd) return;

    const quota = getPositiveQuota(item);
    totalUsage += quota;

    const index = getBucketIndex(timestamp, start, effectiveEnd, bucketCount);
    trend[index] += quota;
  });

  const balance = Math.max(0, Number(currentBalance) || 0);
  const recent24Usage = calculateWindowUsage(
    data,
    effectiveEnd - SECONDS_PER_DAY,
    effectiveEnd,
  );
  const recent48Usage = calculateWindowUsage(
    data,
    effectiveEnd - 2 * SECONDS_PER_DAY,
    effectiveEnd,
  );
  const hourlyBurnQuota = calculateHourlyBurnRate(
    totalUsage,
    recent24Usage,
    recent48Usage,
    lookbackSeconds,
  );
  const dailyBurnQuota = hourlyBurnQuota * 24;

  if (balance <= 0) {
    return {
      status: 'exhausted',
      dailyBurnQuota,
      hourlyBurnQuota,
      daysRemaining: 0,
      secondsRemaining: 0,
      estimatedEmptyAt: new Date(),
      lookbackDays,
      totalUsageQuota: totalUsage,
      recentUsageQuota: recent24Usage,
      trend,
    };
  }

  if (hourlyBurnQuota <= 0) {
    return {
      status: 'idle',
      dailyBurnQuota: 0,
      hourlyBurnQuota: 0,
      daysRemaining: null,
      secondsRemaining: null,
      estimatedEmptyAt: null,
      lookbackDays,
      totalUsageQuota: totalUsage,
      recentUsageQuota: recent24Usage,
      trend,
    };
  }

  const secondsRemaining = (balance / hourlyBurnQuota) * SECONDS_PER_HOUR;
  const daysRemaining = secondsRemaining / SECONDS_PER_DAY;

  return {
    status: 'active',
    dailyBurnQuota,
    hourlyBurnQuota,
    daysRemaining,
    secondsRemaining,
    estimatedEmptyAt: new Date(Date.now() + secondsRemaining * 1000),
    lookbackDays,
    totalUsageQuota: totalUsage,
    recentUsageQuota: recent24Usage,
    trend,
  };
};

export const aggregateDataByTimeAndModel = (data, dataExportDefaultTime) => {
  const aggregatedData = new Map();

  // 检查数据是否跨年
  const showYear = isDataCrossYear(data.map((item) => item.created_at));

  data.forEach((item) => {
    const timeKey = timestamp2string1(
      item.created_at,
      dataExportDefaultTime,
      showYear,
    );
    const modelKey = item.model_name;
    const key = `${timeKey}-${modelKey}`;

    if (!aggregatedData.has(key)) {
      aggregatedData.set(key, {
        time: timeKey,
        model: modelKey,
        quota: 0,
        count: 0,
      });
    }

    const existing = aggregatedData.get(key);
    existing.quota += item.quota;
    existing.count += item.count;
  });

  return aggregatedData;
};

export const generateChartTimePoints = (
  aggregatedData,
  data,
  dataExportDefaultTime,
) => {
  let chartTimePoints = Array.from(
    new Set([...aggregatedData.values()].map((d) => d.time)),
  );

  if (chartTimePoints.length < DEFAULTS.MAX_TREND_POINTS) {
    const lastTime = Math.max(...data.map((item) => item.created_at));
    const interval = getTimeInterval(dataExportDefaultTime, true);

    // 生成时间点数组，用于检查是否跨年
    const generatedTimestamps = Array.from(
      { length: DEFAULTS.MAX_TREND_POINTS },
      (_, i) => lastTime - (6 - i) * interval,
    );
    const showYear = isDataCrossYear(generatedTimestamps);

    chartTimePoints = generatedTimestamps.map((ts) =>
      timestamp2string1(ts, dataExportDefaultTime, showYear),
    );
  }

  return chartTimePoints;
};

// ========== 用户维度数据处理 ==========
export const processUserData = (data, dataExportDefaultTime, limit = 10) => {
  const userQuotaTotal = new Map();
  data.forEach((item) => {
    const prev = userQuotaTotal.get(item.username) || 0;
    userQuotaTotal.set(item.username, prev + item.quota);
  });

  const sorted = Array.from(userQuotaTotal.entries()).sort(
    (a, b) => b[1] - a[1],
  );
  const topUsers = sorted.slice(0, limit).map(([u]) => u);
  const topUserSet = new Set(topUsers);

  const rankingData = sorted.slice(0, limit).map(([username, quota]) => ({
    User: username,
    Quota: quota,
  }));

  const showYear = isDataCrossYear(data.map((item) => item.created_at));

  const timeUserMap = new Map();
  const allTimePoints = new Set();

  data.forEach((item) => {
    const timeKey = timestamp2string1(
      item.created_at,
      dataExportDefaultTime,
      showYear,
    );
    allTimePoints.add(timeKey);
    const user = topUserSet.has(item.username) ? item.username : null;
    if (!user) return;
    const key = `${timeKey}-${user}`;
    const prev = timeUserMap.get(key) || { quota: 0 };
    timeUserMap.set(key, { quota: prev.quota + item.quota });
  });

  const sortedTimePoints = Array.from(allTimePoints).sort();
  const trendData = [];
  sortedTimePoints.forEach((time) => {
    topUsers.forEach((user) => {
      const key = `${time}-${user}`;
      const val = timeUserMap.get(key);
      trendData.push({
        Time: time,
        User: user,
        Quota: val?.quota || 0,
      });
    });
  });

  return { rankingData, trendData, topUsers };
};
