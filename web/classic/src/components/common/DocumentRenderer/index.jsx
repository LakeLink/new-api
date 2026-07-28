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

import React, { useEffect, useState } from 'react';
import { API, showError } from '../../../helpers';
import { Empty, Card, Spin, Typography } from '@douyinfe/semi-ui';
const { Title } = Typography;
import {
  IllustrationConstruction,
  IllustrationConstructionDark,
} from '@douyinfe/semi-illustrations';
import { useTranslation } from 'react-i18next';
import MarkdownRenderer from '../markdown/MarkdownRenderer';
import SafeHtml from '../SafeHtml';
import { normalizeHttpUrl } from '../../../helpers/safeNavigation';

// Check whether content contains HTML.
const isHtmlContent = (content) => {
  if (!content || typeof content !== 'string') return false;

  const htmlTagRegex = /<\/?[a-z][\s\S]*>/i;
  return htmlTagRegex.test(content);
};

/**
 * 通用文档渲染组件
 * @param {string} apiEndpoint - API 接口地址
 * @param {string} title - 文档标题
 * @param {string} cacheKey - 本地存储缓存键
 * @param {string} emptyMessage - 空内容时的提示消息
 */
const DocumentRenderer = ({ apiEndpoint, title, cacheKey, emptyMessage }) => {
  const { t } = useTranslation();
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    const cachedContent = localStorage.getItem(cacheKey) || '';
    if (cachedContent) {
      setContent(cachedContent);
      setLoading(false);
    } else {
      setContent('');
      setLoading(true);
    }

    (async () => {
      try {
        const res = await API.get(apiEndpoint);
        const { success, message, data } = res.data;
        if (success && data) {
          localStorage.setItem(cacheKey, data);
          if (!cancelled) {
            setContent(data);
          }
        } else if (!cachedContent && !cancelled) {
          showError(message || emptyMessage);
          setContent('');
        }
      } catch {
        if (!cachedContent && !cancelled) {
          showError(emptyMessage);
          setContent('');
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [apiEndpoint, cacheKey, emptyMessage]);

  // 显示加载状态
  if (loading) {
    return (
      <div className='classic-page-fill flex justify-center items-center'>
        <Spin size='large' />
      </div>
    );
  }

  // 如果没有内容，显示空状态
  if (!content || content.trim() === '') {
    return (
      <div className='classic-page-fill flex justify-center items-center bg-gray-50'>
        <Empty
          title={t('管理员未设置' + title + '内容')}
          image={
            <IllustrationConstruction style={{ width: 150, height: 150 }} />
          }
          darkModeImage={
            <IllustrationConstructionDark style={{ width: 150, height: 150 }} />
          }
          className='p-8'
        />
      </div>
    );
  }

  // 如果是 URL，显示链接卡片
  const externalUrl = normalizeHttpUrl(content);
  if (externalUrl) {
    return (
      <div className='classic-page-fill flex justify-center items-center bg-gray-50 p-4'>
        <Card className='max-w-md w-full'>
          <div className='text-center'>
            <Title heading={4} className='mb-4'>
              {title}
            </Title>
            <p className='text-gray-600 mb-4'>
              {t('管理员设置了外部链接，点击下方按钮访问')}
            </p>
            <a
              href={externalUrl}
              target='_blank'
              rel='noopener noreferrer'
              title={externalUrl}
              aria-label={`${t('访问' + title)}: ${externalUrl}`}
              className='inline-block px-6 py-3 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors'
            >
              {t('访问' + title)}
            </a>
          </div>
        </Card>
      </div>
    );
  }

  // 如果是 HTML 内容，直接渲染
  if (isHtmlContent(content)) {
    return (
      <div className='classic-page-fill bg-gray-50'>
        <div className='max-w-4xl mx-auto py-12 px-4 sm:px-6 lg:px-8'>
          <div className='bg-white rounded-lg shadow-sm p-8'>
            <Title heading={2} className='text-center mb-8'>
              {title}
            </Title>
            <SafeHtml
              className='prose prose-lg max-w-none'
              content={content}
              isolated
              rich
            />
          </div>
        </div>
      </div>
    );
  }

  // 其他内容统一使用 Markdown 渲染器
  return (
    <div className='classic-page-fill bg-gray-50'>
      <div className='max-w-4xl mx-auto py-12 px-4 sm:px-6 lg:px-8'>
        <div className='bg-white rounded-lg shadow-sm p-8'>
          <Title heading={2} className='text-center mb-8'>
            {title}
          </Title>
          <div className='prose prose-lg max-w-none'>
            <MarkdownRenderer content={content} />
          </div>
        </div>
      </div>
    </div>
  );
};

export default DocumentRenderer;
