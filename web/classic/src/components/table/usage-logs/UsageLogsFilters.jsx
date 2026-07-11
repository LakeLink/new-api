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

import React, { useEffect, useRef, useState } from 'react';
import { Button, Form, Modal, Select, Tooltip } from '@douyinfe/semi-ui';
import { IconDownload, IconHelpCircle, IconSearch } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

import { DATE_RANGE_PRESETS } from '../../../constants/console.constants';
import { API } from '../../../helpers';
import { buildLogFilterExpression } from '../../../helpers/log-expression';

const exportFormatOptions = [
  { value: 'jsonl', label: '导出 JSONL' },
  { value: 'json', label: '导出 JSON' },
  { value: 'csv', label: '导出 CSV' },
];

const exportRowOptions = [
  '100',
  '1000',
  '2000',
  '5000',
  '10000',
  '20000',
  'all',
];

function expressionFieldNames(field) {
  return Array.isArray(field.names)
    ? field.names.join(', ')
    : field.names || field.fields;
}

function expressionFieldTypeKey(type) {
  if (type === 'Number') return '数字';
  if (type === 'String') return '字符串';
  if (type === 'Boolean') return '布尔值';
  return type;
}

function expressionFieldScopeKey(scope) {
  if (scope === 'admin') return '仅管理员';
  if (scope === 'all') return '所有用户';
  return scope;
}

const LogsFilters = ({
  formInitValues,
  setFormApi,
  refresh,
  setShowColumnSelector,
  formApi,
  setLogType,
  exprMode,
  setExprMode,
  exportingFormat,
  exportLogs,
  canExportLogs,
  loading,
  isAdminUser,
  t,
}) => {
  const [exprHelpVisible, setExprHelpVisible] = useState(false);
  const [exportDialogVisible, setExportDialogVisible] = useState(false);
  const [exportFormat, setExportFormat] = useState('jsonl');
  const [exportRowLimit, setExportRowLimit] = useState('10000');
  const [expressionSchema, setExpressionSchema] = useState(null);
  const [expressionSchemaRole, setExpressionSchemaRole] = useState(null);
  const [expressionSchemaLoading, setExpressionSchemaLoading] = useState(false);
  const [expressionSchemaError, setExpressionSchemaError] = useState(false);
  const [expressionSchemaReload, setExpressionSchemaReload] = useState(0);
  const generatedExpressionRef = useRef(null);
  const expressionEditedRef = useRef(false);

  useEffect(() => {
    const role = isAdminUser ? 'admin' : 'user';
    if (
      !exprHelpVisible ||
      (expressionSchema && expressionSchemaRole === role)
    ) {
      return undefined;
    }

    let cancelled = false;
    const controller = new AbortController();
    setExpressionSchemaLoading(true);
    setExpressionSchemaError(false);
    API.get('/api/log/expr/schema', {
      signal: controller.signal,
      disableDuplicate: true,
      skipErrorHandler: true,
    })
      .then((response) => {
        if (!cancelled && response.data?.success && response.data.data) {
          setExpressionSchema(response.data.data);
          setExpressionSchemaRole(role);
        } else if (!cancelled) {
          setExpressionSchemaError(true);
        }
      })
      .catch((error) => {
        if (!cancelled && error?.code !== 'ERR_CANCELED') {
          setExpressionSchemaError(true);
        }
      })
      .finally(() => {
        if (!cancelled) setExpressionSchemaLoading(false);
      });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [
    exprHelpVisible,
    expressionSchema,
    expressionSchemaReload,
    expressionSchemaRole,
    isAdminUser,
  ]);

  const handleExpressionModeToggle = () => {
    if (exprMode) {
      setExprMode(false);
      return;
    }

    if (!expressionEditedRef.current && formApi) {
      const values = formApi.getValues();
      const generated = buildLogFilterExpression(
        values,
        isAdminUser,
        formInitValues.dateRange,
      );
      generatedExpressionRef.current = generated;
      formApi.setValue('expr_search', generated);
    }
    setExprMode(true);
  };

  const handleExpressionEdited = (value) => {
    if (value === generatedExpressionRef.current) {
      generatedExpressionRef.current = null;
      return;
    }
    expressionEditedRef.current = true;
    generatedExpressionRef.current = null;
  };

  const handleExportConfirm = async () => {
    const success = await exportLogs(exportFormat, exportRowLimit);
    if (success) {
      setExportDialogVisible(false);
    }
  };

  return (
    <>
      <Form
        initValues={formInitValues}
        getFormApi={(api) => setFormApi(api)}
        onSubmit={refresh}
        allowEmpty={true}
        autoComplete='off'
        layout='vertical'
        trigger='change'
        stopValidateWithError={false}
      >
        <div className='flex flex-col gap-2'>
          <div
            className={
              exprMode
                ? 'grid grid-cols-1 gap-2'
                : 'grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-2'
            }
          >
            {exprMode ? (
              <Form.Input
                field='expr_search'
                prefix={<IconSearch />}
                placeholder={t(
                  '表达式搜索，例如：model_name contains "gpt" && type == 2',
                )}
                showClear
                pure
                size='small'
                onChange={handleExpressionEdited}
              />
            ) : (
              <>
                {/* 时间选择器 */}
                <div className='col-span-1 lg:col-span-2'>
                  <Form.DatePicker
                    field='dateRange'
                    className='w-full'
                    type='dateTimeRange'
                    placeholder={[t('开始时间'), t('结束时间')]}
                    showClear
                    pure
                    size='small'
                    presets={DATE_RANGE_PRESETS.map((preset) => ({
                      text: t(preset.text),
                      start: preset.start(),
                      end: preset.end(),
                    }))}
                  />
                </div>

                {/* 其他搜索字段 */}
                <Form.Input
                  field='token_name'
                  prefix={<IconSearch />}
                  placeholder={t('令牌名称')}
                  showClear
                  pure
                  size='small'
                />

                <Form.Input
                  field='model_name'
                  prefix={<IconSearch />}
                  placeholder={t('模型名称')}
                  showClear
                  pure
                  size='small'
                />

                <Form.Input
                  field='group'
                  prefix={<IconSearch />}
                  placeholder={t('分组')}
                  showClear
                  pure
                  size='small'
                />

                <Form.Input
                  field='request_id'
                  prefix={<IconSearch />}
                  placeholder={t('Request ID')}
                  showClear
                  pure
                  size='small'
                />

                <Form.Input
                  field='upstream_request_id'
                  prefix={<IconSearch />}
                  placeholder={t('Upstream Request ID')}
                  showClear
                  pure
                  size='small'
                />

                {isAdminUser && (
                  <>
                    <Form.Input
                      field='channel'
                      prefix={<IconSearch />}
                      placeholder={t('渠道 ID')}
                      showClear
                      pure
                      size='small'
                    />
                    <Form.Input
                      field='username'
                      prefix={<IconSearch />}
                      placeholder={t('用户名称')}
                      showClear
                      pure
                      size='small'
                    />
                  </>
                )}
              </>
            )}
          </div>

          {/* 操作按钮区域 */}
          <div className='flex flex-col sm:flex-row justify-between items-start sm:items-center gap-3'>
            {/* 日志类型选择器 */}
            <div className='w-full sm:w-auto'>
              {!exprMode && (
                <Form.Select
                  field='logType'
                  placeholder={t('日志类型')}
                  className='w-full sm:w-auto min-w-[120px]'
                  showClear
                  pure
                  onChange={() => {
                    // 延迟执行搜索，让表单值先更新
                    setTimeout(() => {
                      refresh();
                    }, 0);
                  }}
                  size='small'
                >
                  <Form.Select.Option value='0'>{t('全部')}</Form.Select.Option>
                  <Form.Select.Option value='1'>{t('充值')}</Form.Select.Option>
                  <Form.Select.Option value='2'>{t('消费')}</Form.Select.Option>
                  <Form.Select.Option value='3'>{t('管理')}</Form.Select.Option>
                  <Form.Select.Option value='4'>{t('系统')}</Form.Select.Option>
                  <Form.Select.Option value='5'>{t('错误')}</Form.Select.Option>
                  <Form.Select.Option value='6'>{t('退款')}</Form.Select.Option>
                  <Form.Select.Option value='7'>{t('登录')}</Form.Select.Option>
                </Form.Select>
              )}
            </div>

            <div className='flex gap-2 w-full sm:w-auto justify-end'>
              <Button
                type={exprMode ? 'primary' : 'tertiary'}
                onClick={handleExpressionModeToggle}
                size='small'
              >
                {exprMode ? t('字段搜索') : t('表达式搜索')}
              </Button>
              <Tooltip content={t('表达式搜索帮助')}>
                <Button
                  type='tertiary'
                  icon={<IconHelpCircle />}
                  onClick={() => setExprHelpVisible(true)}
                  aria-label={t('表达式搜索帮助')}
                  size='small'
                />
              </Tooltip>
              {canExportLogs && (
                <Button
                  type='tertiary'
                  icon={<IconDownload />}
                  loading={!!exportingFormat}
                  onClick={() => setExportDialogVisible(true)}
                  size='small'
                >
                  {t('导出')}
                </Button>
              )}
              <Button
                type='tertiary'
                htmlType='submit'
                loading={loading}
                size='small'
              >
                {t('查询')}
              </Button>
              <Button
                type='tertiary'
                onClick={() => {
                  if (formApi) {
                    formApi.reset();
                    generatedExpressionRef.current = null;
                    expressionEditedRef.current = false;
                    setLogType(0);
                    setExprMode(false);
                    setTimeout(() => {
                      refresh();
                    }, 100);
                  }
                }}
                size='small'
              >
                {t('重置')}
              </Button>
              <Button
                type='tertiary'
                onClick={() => setShowColumnSelector(true)}
                size='small'
              >
                {t('列设置')}
              </Button>
            </div>
          </div>
        </div>
      </Form>

      <ExpressionSearchHelpModal
        visible={exprHelpVisible}
        onCancel={() => setExprHelpVisible(false)}
        schema={
          expressionSchemaRole === (isAdminUser ? 'admin' : 'user')
            ? expressionSchema
            : null
        }
        loading={expressionSchemaLoading}
        loadError={expressionSchemaError}
        onRetry={() => setExpressionSchemaReload((value) => value + 1)}
        isAdminUser={isAdminUser}
        t={t}
      />

      <Modal
        title={t('导出调用日志')}
        visible={exportDialogVisible}
        onCancel={() => setExportDialogVisible(false)}
        footer={
          <div className='flex justify-end gap-2'>
            <Button
              onClick={() => setExportDialogVisible(false)}
              disabled={!!exportingFormat}
            >
              {t('取消')}
            </Button>
            <Button
              type='primary'
              icon={<IconDownload />}
              loading={!!exportingFormat}
              onClick={handleExportConfirm}
            >
              {t('导出')}
            </Button>
          </div>
        }
      >
        <div className='space-y-4'>
          <div className='space-y-2'>
            <div className='text-sm font-medium'>{t('导出格式')}</div>
            <Select
              className='w-full'
              value={exportFormat}
              disabled={!!exportingFormat}
              onChange={(value) => setExportFormat(value)}
            >
              {exportFormatOptions.map((option) => (
                <Select.Option key={option.value} value={option.value}>
                  {t(option.label)}
                </Select.Option>
              ))}
            </Select>
          </div>
          <div className='space-y-2'>
            <div className='text-sm font-medium'>{t('导出行数')}</div>
            <Select
              className='w-full'
              value={exportRowLimit}
              disabled={!!exportingFormat}
              onChange={(value) => setExportRowLimit(value)}
            >
              {exportRowOptions.map((option) => (
                <Select.Option key={option} value={option}>
                  {option === 'all' ? t('全部') : option}
                </Select.Option>
              ))}
            </Select>
          </div>
        </div>
      </Modal>
    </>
  );
};

const tableHeaderStyle = {
  borderBottom: '1px solid var(--semi-color-border)',
};

const tableCellStyle = {
  borderTop: '1px solid var(--semi-color-border)',
};

function ExpressionSearchHelpModal({
  visible,
  onCancel,
  schema,
  loading,
  loadError,
  onRetry,
  isAdminUser,
  t,
}) {
  const { i18n } = useTranslation();
  const schemaText = (english, chinese) => {
    if (i18n.resolvedLanguage?.startsWith('zh')) {
      return chinese || english;
    }
    if (chinese) {
      const translated = t(chinese);
      if (translated !== chinese) return translated;
    }
    return english;
  };
  const fields = (schema?.fields || []).filter(
    (field) => field.scope !== 'admin' || isAdminUser,
  );
  const examples = (schema?.examples || []).filter(
    (example) => example.scope !== 'admin' || isAdminUser,
  );

  if (!schema) {
    return (
      <Modal
        title={t('表达式搜索参考')}
        visible={visible}
        onCancel={onCancel}
        footer={null}
        width={920}
      >
        <div className='flex min-h-32 items-center justify-center gap-3'>
          <span>{t(loadError ? '加载失败' : '加载中...')}</span>
          {loadError && (
            <Button type='tertiary' loading={loading} onClick={onRetry}>
              {t('重试')}
            </Button>
          )}
        </div>
      </Modal>
    );
  }

  return (
    <Modal
      title={t('表达式搜索参考')}
      visible={visible}
      onCancel={onCancel}
      footer={null}
      width={920}
      bodyStyle={{ maxHeight: '72vh', overflowY: 'auto' }}
    >
      <div className='space-y-5 text-sm'>
        <section className='space-y-2'>
          <p className='text-gray-500'>
            {schemaText(schema.intro, schema.introZh)}
          </p>
        </section>

        <section className='space-y-2'>
          <h3 className='font-medium'>{t('快速语法')}</h3>
          <p className='text-gray-500'>
            {schemaText(schema.quickSyntax, schema.quickSyntaxZh)}
          </p>
        </section>

        <section className='space-y-2'>
          <h3 className='font-medium'>{t('可用字段')}</h3>
          <div className='overflow-x-auto rounded-md border border-gray-200'>
            <table className='w-full text-left text-sm'>
              <thead>
                <tr>
                  <th
                    className='px-3 py-2 font-medium'
                    style={tableHeaderStyle}
                  >
                    {t('字段')}
                  </th>
                  <th
                    className='px-3 py-2 font-medium'
                    style={tableHeaderStyle}
                  >
                    {t('类型')}
                  </th>
                  <th
                    className='px-3 py-2 font-medium'
                    style={tableHeaderStyle}
                  >
                    {t('可用范围')}
                  </th>
                  <th
                    className='px-3 py-2 font-medium'
                    style={tableHeaderStyle}
                  >
                    {t('说明')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {fields.map((field) => (
                  <tr key={expressionFieldNames(field)}>
                    <td className='px-3 py-2 align-top' style={tableCellStyle}>
                      <code className='rounded bg-gray-100 px-1.5 py-0.5 text-xs'>
                        {expressionFieldNames(field)}
                      </code>
                    </td>
                    <td className='px-3 py-2 align-top' style={tableCellStyle}>
                      {t(expressionFieldTypeKey(field.type))}
                    </td>
                    <td className='px-3 py-2 align-top' style={tableCellStyle}>
                      {t(expressionFieldScopeKey(field.scope))}
                    </td>
                    <td
                      className='px-3 py-2 align-top text-gray-500'
                      style={tableCellStyle}
                    >
                      {schemaText(field.description, field.descriptionZh)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className='space-y-2'>
          <h3 className='font-medium'>{t('操作符')}</h3>
          <div className='overflow-x-auto rounded-md border border-gray-200'>
            <table className='w-full text-left text-sm'>
              <thead>
                <tr>
                  <th
                    className='px-3 py-2 font-medium'
                    style={tableHeaderStyle}
                  >
                    {t('操作符')}
                  </th>
                  <th
                    className='px-3 py-2 font-medium'
                    style={tableHeaderStyle}
                  >
                    {t('用法')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {(schema?.operators || []).map((operator) => (
                  <tr key={operator.syntax}>
                    <td className='px-3 py-2 align-top' style={tableCellStyle}>
                      <code className='rounded bg-gray-100 px-1.5 py-0.5 text-xs'>
                        {operator.syntax}
                      </code>
                    </td>
                    <td
                      className='px-3 py-2 align-top text-gray-500'
                      style={tableCellStyle}
                    >
                      {schemaText(operator.description, operator.descriptionZh)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className='space-y-2'>
          <h3 className='font-medium'>{t('常用表达式')}</h3>
          <div className='grid grid-cols-1 gap-3 md:grid-cols-2'>
            {examples.map((example) => (
              <div
                key={example.title}
                className='rounded-md border border-gray-200 p-3'
              >
                <div className='font-medium'>
                  {schemaText(example.title, example.titleZh)}
                </div>
                <code className='mt-2 block break-all rounded bg-gray-100 px-2 py-1.5 text-xs'>
                  {example.expression}
                </code>
                <p className='mt-2 text-gray-500'>
                  {schemaText(example.description, example.descriptionZh)}
                </p>
              </div>
            ))}
          </div>
        </section>

        <section className='space-y-2'>
          <h3 className='font-medium'>{t('安全和限制')}</h3>
          <ul className='list-disc space-y-1 pl-5 text-gray-500'>
            <li>{schemaText(schema.safety, schema.safetyZh)}</li>
            <li>
              {i18n.resolvedLanguage?.startsWith('zh')
                ? `表达式最多 ${schema.limits.maxLength} 个字符，语法树最多 ${schema.limits.maxNodes} 个节点，字符串字面量最多 ${schema.limits.maxStringLength} 个字符，in 数组最多 ${schema.limits.maxInItems} 项。`
                : `Expressions are limited to ${schema.limits.maxLength} characters and ${schema.limits.maxNodes} syntax nodes; string literals are limited to ${schema.limits.maxStringLength} characters and in arrays to ${schema.limits.maxInItems} items.`}
            </li>
          </ul>
        </section>
      </div>
    </Modal>
  );
}

export default LogsFilters;
