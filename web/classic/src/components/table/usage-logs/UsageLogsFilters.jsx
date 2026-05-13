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

import React, { useState } from 'react';
import { Button, Form, Modal, Select, Tooltip } from '@douyinfe/semi-ui';
import { IconDownload, IconHelpCircle, IconSearch } from '@douyinfe/semi-icons';

import { DATE_RANGE_PRESETS } from '../../../constants/console.constants';

const exprFieldRows = [
  {
    fields: 'id',
    type: '数字',
    scope: '所有用户',
    description: '日志记录 ID。',
  },
  {
    fields: 'user_id',
    type: '数字',
    scope: '所有用户',
    description: '拥有该日志的用户 ID。',
  },
  {
    fields: 'created_at, createdAt, timestamp',
    type: '数字',
    scope: '所有用户',
    description: '创建时间，Unix 秒级时间戳。',
  },
  {
    fields: 'type, log_type',
    type: '数字',
    scope: '所有用户',
    description: '日志类型：1 充值，2 消费，3 管理，4 系统，5 错误，6 退款。',
  },
  {
    fields: 'content',
    type: '字符串',
    scope: '所有用户',
    description: '主要日志内容或错误消息文本。',
  },
  {
    fields: 'token_name, token',
    type: '字符串',
    scope: '所有用户',
    description: '日志中记录的 API 令牌名称。',
  },
  {
    fields: 'model_name, model',
    type: '字符串',
    scope: '所有用户',
    description: '请求的模型名称。',
  },
  {
    fields: 'quota',
    type: '数字',
    scope: '所有用户',
    description: '本次记录扣费额度。',
  },
  {
    fields: 'prompt_tokens',
    type: '数字',
    scope: '所有用户',
    description: '提示词或输入 token 数。',
  },
  {
    fields: 'completion_tokens',
    type: '数字',
    scope: '所有用户',
    description: '补全或输出 token 数。',
  },
  {
    fields: 'use_time',
    type: '数字',
    scope: '所有用户',
    description: '响应耗时，单位为秒。',
  },
  {
    fields: 'is_stream, stream',
    type: '布尔值',
    scope: '所有用户',
    description: '请求是否使用流式响应。',
  },
  {
    fields: 'token_id',
    type: '数字',
    scope: '所有用户',
    description: 'API 令牌的数字 ID。',
  },
  {
    fields: 'group',
    type: '字符串',
    scope: '所有用户',
    description: '计费或请求分组名称。',
  },
  {
    fields: 'ip',
    type: '字符串',
    scope: '所有用户',
    description: '开启 IP 记录后保存的客户端 IP 地址。',
  },
  {
    fields: 'request_id, requestId',
    type: '字符串',
    scope: '所有用户',
    description: '用于追踪单次调用的 Request ID。',
  },
  {
    fields: 'other',
    type: '字符串',
    scope: '所有用户',
    description: '随日志保存的额外元数据。',
  },
  {
    fields: 'username',
    type: '字符串',
    scope: '仅管理员',
    description: '日志关联的用户名。',
  },
  {
    fields: 'channel, channel_id',
    type: '数字',
    scope: '仅管理员',
    description: '请求使用的渠道 ID。',
  },
  {
    fields: 'channel_name, channelName',
    type: '字符串',
    scope: '仅管理员',
    description: '请求使用的渠道名称。',
  },
];

const exprOperatorRows = [
  {
    syntax: '&&, ||, !, and, or, not',
    description: '使用布尔逻辑和括号组合多个条件。',
  },
  {
    syntax: '==, !=, >, >=, <, <=',
    description: '将字段与字符串、整数、布尔值或 nil 字面量比较。',
  },
  {
    syntax: 'contains, startsWith, endsWith',
    description: '对字符串字段执行 SQL LIKE 匹配，并自动转义通配符。',
  },
  {
    syntax: 'in, not in',
    description: '将字段与最多 100 个字面量组成的数组进行匹配。',
  },
  {
    syntax: 'nil',
    description: 'nil 只能与 == 或 != 一起用于空值判断。',
  },
];

const exprExamples = [
  {
    title: 'GPT 消费日志',
    expression: 'model_name contains "gpt" && type == 2',
    description: '查找 GPT 系列模型的消费记录。',
  },
  {
    title: '按一天时间戳筛选',
    expression: 'created_at >= 1735689600 && created_at <= 1735775999',
    description: '将时间戳替换为目标日期的开始和结束秒数。',
  },
  {
    title: '高额度消费',
    expression: 'quota > 1000 && type == 2',
    description: '查找额度消耗较高的消费记录。',
  },
  {
    title: '大 token 请求',
    expression: 'prompt_tokens > 8000 || completion_tokens > 2000',
    description: '查找输入或输出 token 数异常大的调用。',
  },
  {
    title: '流式 Claude 调用',
    expression: 'is_stream == true && model_name contains "claude"',
    description: '查找 Claude 系列模型的流式请求。',
  },
  {
    title: '单个 Request ID',
    expression: 'request_id == "req_xxx"',
    description: '直接定位一条可追踪请求。',
  },
  {
    title: '指定分组',
    expression: 'group in ["default", "vip"]',
    description: '查找属于多个分组之一的记录。',
  },
  {
    title: '模型家族',
    expression:
      'model_name startsWith "gpt-4" || model_name startsWith "claude"',
    description: '在同一次搜索中比较多个模型前缀。',
  },
  {
    title: '错误和限流',
    expression: 'content contains "timeout" || other contains "429"',
    description: '查找超时消息或上游限流元数据。',
  },
  {
    title: '排除 Embedding',
    expression: 'not (model_name contains "embedding") && type == 2',
    description: '保留普通消费日志并隐藏 embedding 调用。',
  },
  {
    title: '某时间后的具名令牌',
    expression: 'token_name != "" && created_at >= 1735689600',
    description: '查找某个时间戳之后带令牌名称的日志。',
  },
  {
    title: '单个客户端 IP',
    expression: 'ip == "1.2.3.4"',
    description: '查找来自某个客户端 IP 的请求。',
  },
  {
    title: '管理员：用户和渠道',
    expression: 'username == "alice" && channel == 12',
    description: '管理员可按用户名和数字渠道 ID 筛选。',
  },
  {
    title: '管理员：渠道名称',
    expression: 'channel_name contains "openai" && type == 2',
    description: '管理员可按渠道名称和日志类型筛选。',
  },
];

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
                </Form.Select>
              )}
            </div>

            <div className='flex gap-2 w-full sm:w-auto justify-end'>
              <Button
                type={exprMode ? 'primary' : 'tertiary'}
                onClick={() => setExprMode(!exprMode)}
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

function ExpressionSearchHelpModal({ visible, onCancel, t }) {
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
            {t(
              '表达式搜索会从 AST 解析表达式，并使用允许字段列表、SQL 参数占位符和转义后的 LIKE 模式生成查询。',
            )}
          </p>
        </section>

        <section className='space-y-2'>
          <h3 className='font-medium'>{t('快速语法')}</h3>
          <ul className='list-disc space-y-1 pl-5 text-gray-500'>
            <li>
              {t(
                '字符串使用双引号，数字为整数，布尔值为 true 或 false，nil 可用于空值判断。',
              )}
            </li>
            <li>
              {t(
                '使用括号组合逻辑，例如 not (model_name contains "embedding") && type == 2。',
              )}
            </li>
            <li>
              {t(
                '布尔字段可以直接写 is_stream，也可以显式比较 true 或 false。',
              )}
            </li>
          </ul>
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
                {exprFieldRows.map((field) => (
                  <tr key={field.fields}>
                    <td className='px-3 py-2 align-top' style={tableCellStyle}>
                      <code className='rounded bg-gray-100 px-1.5 py-0.5 text-xs'>
                        {field.fields}
                      </code>
                    </td>
                    <td className='px-3 py-2 align-top' style={tableCellStyle}>
                      {t(field.type)}
                    </td>
                    <td className='px-3 py-2 align-top' style={tableCellStyle}>
                      {t(field.scope)}
                    </td>
                    <td
                      className='px-3 py-2 align-top text-gray-500'
                      style={tableCellStyle}
                    >
                      {t(field.description)}
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
                {exprOperatorRows.map((operator) => (
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
                      {t(operator.description)}
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
            {exprExamples.map((example) => (
              <div
                key={example.title}
                className='rounded-md border border-gray-200 p-3'
              >
                <div className='font-medium'>{t(example.title)}</div>
                <code className='mt-2 block break-all rounded bg-gray-100 px-2 py-1.5 text-xs'>
                  {example.expression}
                </code>
                <p className='mt-2 text-gray-500'>{t(example.description)}</p>
              </div>
            ))}
          </div>
        </section>

        <section className='space-y-2'>
          <h3 className='font-medium'>{t('安全和限制')}</h3>
          <ul className='list-disc space-y-1 pl-5 text-gray-500'>
            <li>
              {t('只接受上方列出的字段，未知标识符会在生成 SQL 前被拒绝。')}
            </li>
            <li>
              {t(
                '所有值都会作为 SQL 参数绑定，LIKE 搜索会转义 %、_ 和 ! 字符。',
              )}
            </li>
            <li>
              {t(
                '表达式最多 4096 个字符，字符串字面量最多 1024 个字符，in 数组最多 100 项。',
              )}
            </li>
            <li>
              {t('不支持正则 matches、算术运算、函数调用和字段之间的比较。')}
            </li>
          </ul>
        </section>
      </div>
    </Modal>
  );
}

export default LogsFilters;
