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
import React, { useEffect, useState, useCallback, useMemo } from 'react';
import {
  Button,
  Collapsible,
  Radio,
  RadioGroup,
  Select,
  Switch,
  Tag,
  Typography,
  Popconfirm,
} from '@douyinfe/semi-ui';
import {
  IconChevronDown,
  IconChevronUp,
  IconDelete,
  IconPlus,
} from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

let _idCounter = 0;
const uid = () => `gf_${++_idCounter}`;

const TARGET_RATIO_MODE_OPTIONS = [
  {
    value: 'origin_special',
    labelKey: '仅使用源分组特殊倍率',
  },
  {
    value: 'target_special',
    labelKey: '仅使用目标分组特殊倍率',
  },
  {
    value: 'normal_only',
    labelKey: '仅使用目标分组普通倍率',
  },
  {
    value: 'prefer_origin_special',
    labelKey: '优先使用源分组特殊倍率',
  },
  {
    value: 'prefer_target_special',
    labelKey: '优先使用目标分组特殊倍率',
  },
];

function normalizeTargetPricingRatioMode(value) {
  return TARGET_RATIO_MODE_OPTIONS.some((option) => option.value === value)
    ? value
    : 'target_special';
}

function getTargetPricingRatioModeLabel(mode, t) {
  const normalized = normalizeTargetPricingRatioMode(mode);
  const option = TARGET_RATIO_MODE_OPTIONS.find(
    (item) => item.value === normalized,
  );
  return t(option?.labelKey || '仅使用目标分组特殊倍率');
}

function parseJSON(str) {
  if (!str || !str.trim()) return {};
  try {
    return JSON.parse(str);
  } catch {
    return {};
  }
}

function normalizeRule(rule) {
  const rawRule = typeof rule === 'object' && rule !== null ? rule : {};
  const rawFallback = rawRule.fallback;
  const fallback = Array.isArray(rawFallback)
    ? rawFallback
        .filter((item) => typeof item === 'string')
        .map((item) => item.trim())
        .filter(Boolean)
    : [];
  const pricingMode = rawRule.pricing_mode === 'origin' ? 'origin' : 'target';
  const originPricingUseSpecialRatio =
    typeof rawRule.origin_pricing_use_special_ratio === 'boolean'
      ? rawRule.origin_pricing_use_special_ratio
      : true;
  const targetPricingRatioMode = normalizeTargetPricingRatioMode(
    rawRule.target_pricing_ratio_mode,
  );
  const extra = { ...rawRule };
  delete extra.fallback;
  delete extra.pricing_mode;
  delete extra.origin_pricing_use_special_ratio;
  delete extra.target_pricing_ratio_mode;
  return {
    fallback,
    pricingMode,
    originPricingUseSpecialRatio,
    targetPricingRatioMode,
    extra,
  };
}

function flattenRules(raw) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return [];
  const rules = [];
  Object.entries(raw).forEach(([sourceGroup, rule]) => {
    if (typeof sourceGroup !== 'string' || !sourceGroup.trim()) return;
    const normalized = normalizeRule(rule);
    rules.push({
      _id: uid(),
      sourceGroup,
      pricingMode: normalized.pricingMode,
      originPricingUseSpecialRatio: normalized.originPricingUseSpecialRatio,
      targetPricingRatioMode: normalized.targetPricingRatioMode,
      extra: normalized.extra,
      fallbackGroups: normalized.fallback.map((name) => ({
        _id: uid(),
        name,
      })),
    });
  });
  return rules;
}

function serializeRules(rules) {
  const result = {};
  rules.forEach((rule) => {
    const sourceGroup = (rule.sourceGroup || '').trim();
    if (!sourceGroup) return;
    const groups = rule.fallbackGroups
      .map((item) => (typeof item.name === 'string' ? item.name.trim() : ''))
      .filter(Boolean);
    result[sourceGroup] = {
      ...(rule.extra || {}),
      fallback: groups,
      pricing_mode: rule.pricingMode === 'origin' ? 'origin' : 'target',
      origin_pricing_use_special_ratio:
        rule.originPricingUseSpecialRatio !== false,
      target_pricing_ratio_mode: normalizeTargetPricingRatioMode(
        rule.targetPricingRatioMode,
      ),
    };
  });
  return Object.keys(result).length === 0
    ? ''
    : JSON.stringify(result, null, 2);
}

function FallbackGroupSection({
  sourceGroup,
  items,
  pricingMode,
  originPricingUseSpecialRatio,
  targetPricingRatioMode,
  groupOptions,
  onUpdate,
  onRemoveRule,
  onAddRule,
  onMoveRule,
  onUpdatePricingMode,
  onUpdateOriginPricingUseSpecialRatio,
  onUpdateTargetPricingRatioMode,
  t,
}) {
  const [open, setOpen] = useState(false);
  const ratioModeLabel =
    pricingMode === 'origin'
      ? originPricingUseSpecialRatio === false
        ? t('仅使用源分组普通倍率')
        : t('使用源分组特殊倍率')
      : getTargetPricingRatioModeLabel(targetPricingRatioMode, t);

  return (
    <div
      style={{
        border: '1px solid var(--semi-color-border)',
        borderRadius: 8,
        overflow: 'hidden',
      }}
    >
      <div
        className='flex items-center justify-between cursor-pointer'
        style={{ padding: '8px 12px', background: 'var(--semi-color-fill-0)' }}
        onClick={() => setOpen(!open)}
      >
        <div className='flex items-center gap-2'>
          {open ? (
            <IconChevronUp size='small' />
          ) : (
            <IconChevronDown size='small' />
          )}
          <Text strong>{sourceGroup}</Text>
          <Tag size='small' color='blue'>
            {items.length} {t('条规则')}
          </Tag>
          <Tag size='small' color={pricingMode === 'origin' ? 'green' : 'cyan'}>
            {pricingMode === 'origin' ? t('源分组定价') : t('目标分组定价')}
          </Tag>
          <Text type='tertiary' size='small'>
            {ratioModeLabel}
          </Text>
        </div>
        <div
          className='flex items-center gap-1'
          onClick={(e) => e.stopPropagation()}
        >
          <Popconfirm
            title={t('确认删除该分组回退？')}
            onConfirm={() => onRemoveRule('_all')}
            position='left'
          >
            <Button
              icon={<IconDelete />}
              size='small'
              type='danger'
              theme='borderless'
            />
          </Popconfirm>
        </div>
      </div>
      <Collapsible isOpen={open} keepDOM>
        <div style={{ padding: '8px 12px' }}>
          <div className='flex items-center gap-2' style={{ marginBottom: 8 }}>
            <Text type='tertiary' size='small'>
              {t('计费模式')}
            </Text>
            <RadioGroup
              type='button'
              size='small'
              value={pricingMode}
              onChange={(e) => onUpdatePricingMode(sourceGroup, e.target.value)}
            >
              <Radio value='target'>{t('目标分组定价')}</Radio>
              <Radio value='origin'>{t('源分组定价')}</Radio>
            </RadioGroup>
          </div>
          <div
            style={{
              border: '1px solid var(--semi-color-border)',
              borderRadius: 6,
              padding: '10px 12px',
              marginBottom: 10,
              background: 'var(--semi-color-fill-0)',
            }}
          >
            <div
              className='flex items-center gap-2'
              style={{ marginBottom: 6 }}
            >
              <Text type='tertiary' size='small'>
                {t('特殊倍率处理')}
              </Text>
              <Tag size='small' color='white'>
                {ratioModeLabel}
              </Tag>
            </div>
            {pricingMode === 'origin' ? (
              <>
                <div className='flex items-center justify-between gap-3'>
                  <Text size='small'>{t('源分组计价使用特殊倍率')}</Text>
                  <Switch
                    size='small'
                    checked={originPricingUseSpecialRatio !== false}
                    checkedText='｜'
                    uncheckedText='〇'
                    onChange={(checked) =>
                      onUpdateOriginPricingUseSpecialRatio(
                        sourceGroup,
                        checked === true,
                      )
                    }
                  />
                </div>
                <Text
                  type='tertiary'
                  size='small'
                  style={{ display: 'block', marginTop: 6 }}
                >
                  {t(
                    '启用后，源分组计价会在已配置时使用用户对源分组的特殊倍率；否则使用源分组普通倍率。',
                  )}
                </Text>
              </>
            ) : (
              <>
                <Select
                  size='small'
                  value={normalizeTargetPricingRatioMode(
                    targetPricingRatioMode,
                  )}
                  optionList={TARGET_RATIO_MODE_OPTIONS.map((option) => ({
                    value: option.value,
                    label: t(option.labelKey),
                  }))}
                  onChange={(value) =>
                    onUpdateTargetPricingRatioMode(
                      sourceGroup,
                      normalizeTargetPricingRatioMode(value),
                    )
                  }
                  style={{ width: '100%' }}
                  position='bottomLeft'
                />
                <Text
                  type='tertiary'
                  size='small'
                  style={{ display: 'block', marginTop: 6 }}
                >
                  {t('选择使用目标分组定价时如何应用用户特殊倍率。')}
                </Text>
              </>
            )}
          </div>
          {items.map((rule, index) => (
            <div
              key={rule._id}
              className='flex items-center gap-2'
              style={{ marginBottom: 6 }}
            >
              <Select
                size='small'
                filter
                allowCreate
                value={rule.name || undefined}
                placeholder={t('选择回退分组')}
                optionList={groupOptions}
                onChange={(value) => onUpdate(rule._id, 'name', value)}
                style={{ flex: 1 }}
                position='bottomLeft'
              />
              <Button
                icon={<IconChevronUp />}
                theme='borderless'
                size='small'
                disabled={index === 0}
                onClick={() => onMoveRule(rule._id, 'up')}
              />
              <Button
                icon={<IconChevronDown />}
                theme='borderless'
                size='small'
                disabled={index === items.length - 1}
                onClick={() => onMoveRule(rule._id, 'down')}
              />
              <Popconfirm
                title={t('确认删除该回退分组？')}
                onConfirm={() => onRemoveRule(rule._id)}
                position='left'
              >
                <Button
                  icon={<IconDelete />}
                  size='small'
                  type='danger'
                  theme='borderless'
                />
              </Popconfirm>
            </div>
          ))}
          <div className='mt-2'>
            <Button
              icon={<IconPlus />}
              theme='outline'
              size='small'
              onClick={onAddRule}
            >
              {t('添加回退分组')}
            </Button>
          </div>
        </div>
      </Collapsible>
    </div>
  );
}

export default function GroupFallbackRules({
  value,
  groupNames = [],
  onChange,
}) {
  const { t } = useTranslation();
  const [rules, setRules] = useState(() => flattenRules(parseJSON(value)));
  const [newSourceGroup, setNewSourceGroup] = useState('');

  useEffect(() => {
    setRules(flattenRules(parseJSON(value)));
  }, [value]);

  const emitChange = useCallback(
    (newRules) => {
      setRules(newRules);
      onChange?.(serializeRules(newRules));
    },
    [onChange],
  );

  const groupOptions = useMemo(
    () => groupNames.map((n) => ({ value: n, label: n })),
    [groupNames],
  );

  const addRuleToSourceGroup = useCallback(
    (sourceGroup) => {
      emitChange(
        rules.map((rule) => {
          if (rule.sourceGroup !== sourceGroup) return rule;
          return {
            ...rule,
            fallbackGroups: [...rule.fallbackGroups, { _id: uid(), name: '' }],
          };
        }),
      );
    },
    [rules, emitChange],
  );

  const updateFallbackGroup = useCallback(
    (id, field, value) => {
      emitChange(
        rules.map((rule) => ({
          ...rule,
          fallbackGroups: rule.fallbackGroups.map((item) =>
            item._id === id ? { ...item, [field]: value } : item,
          ),
        })),
      );
    },
    [rules, emitChange],
  );

  const removeFallbackGroup = useCallback(
    (id) => {
      emitChange(
        rules.map((rule) => ({
          ...rule,
          fallbackGroups: rule.fallbackGroups.filter((item) => item._id !== id),
        })),
      );
    },
    [rules, emitChange],
  );

  const moveFallbackGroup = useCallback(
    (id, direction) => {
      emitChange(
        rules.map((rule) => {
          const index = rule.fallbackGroups.findIndex(
            (item) => item._id === id,
          );
          if (index < 0) return rule;
          const next = [...rule.fallbackGroups];
          if (direction === 'up' && index > 0) {
            [next[index - 1], next[index]] = [next[index], next[index - 1]];
          }
          if (direction === 'down' && index < next.length - 1) {
            [next[index + 1], next[index]] = [next[index], next[index + 1]];
          }
          return { ...rule, fallbackGroups: next };
        }),
      );
    },
    [rules, emitChange],
  );

  const removeSourceGroup = useCallback(
    (sourceGroup) => {
      emitChange(rules.filter((rule) => rule.sourceGroup !== sourceGroup));
    },
    [rules, emitChange],
  );

  const updatePricingMode = useCallback(
    (sourceGroup, pricingMode) => {
      emitChange(
        rules.map((rule) =>
          rule.sourceGroup === sourceGroup ? { ...rule, pricingMode } : rule,
        ),
      );
    },
    [rules, emitChange],
  );

  const updateOriginPricingUseSpecialRatio = useCallback(
    (sourceGroup, originPricingUseSpecialRatio) => {
      emitChange(
        rules.map((rule) =>
          rule.sourceGroup === sourceGroup
            ? { ...rule, originPricingUseSpecialRatio }
            : rule,
        ),
      );
    },
    [rules, emitChange],
  );

  const updateTargetPricingRatioMode = useCallback(
    (sourceGroup, targetPricingRatioMode) => {
      emitChange(
        rules.map((rule) =>
          rule.sourceGroup === sourceGroup
            ? {
                ...rule,
                targetPricingRatioMode: normalizeTargetPricingRatioMode(
                  targetPricingRatioMode,
                ),
              }
            : rule,
        ),
      );
    },
    [rules, emitChange],
  );

  const addSourceGroup = useCallback(() => {
    const sourceGroup = newSourceGroup.trim();
    if (!sourceGroup) return;
    if (rules.some((rule) => rule.sourceGroup === sourceGroup)) {
      setNewSourceGroup('');
      return;
    }
    emitChange([
      ...rules,
      {
        _id: uid(),
        sourceGroup,
        pricingMode: 'target',
        originPricingUseSpecialRatio: true,
        targetPricingRatioMode: 'target_special',
        extra: {},
        fallbackGroups: [],
      },
    ]);
    setNewSourceGroup('');
  }, [rules, emitChange, newSourceGroup]);

  if (!rules.length) {
    return (
      <div>
        <Text type='tertiary' className='block text-center py-4'>
          {t('暂无分组回退规则')}
        </Text>
        <div className='mt-2 flex justify-center gap-2'>
          <Select
            size='small'
            filter
            allowCreate
            placeholder={t('选择源分组')}
            optionList={groupOptions}
            value={newSourceGroup || undefined}
            onChange={setNewSourceGroup}
            style={{ width: 200 }}
            position='bottomLeft'
          />
          <Button icon={<IconPlus />} theme='outline' onClick={addSourceGroup}>
            {t('添加分组回退')}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className='space-y-2'>
      {rules.map((rule) => (
        <FallbackGroupSection
          key={rule._id}
          sourceGroup={rule.sourceGroup}
          items={rule.fallbackGroups}
          pricingMode={rule.pricingMode}
          originPricingUseSpecialRatio={rule.originPricingUseSpecialRatio}
          targetPricingRatioMode={rule.targetPricingRatioMode}
          groupOptions={groupOptions}
          onUpdate={updateFallbackGroup}
          onRemoveRule={(id) =>
            id === '_all'
              ? removeSourceGroup(rule.sourceGroup)
              : removeFallbackGroup(id)
          }
          onAddRule={() => addRuleToSourceGroup(rule.sourceGroup)}
          onMoveRule={moveFallbackGroup}
          onUpdatePricingMode={updatePricingMode}
          onUpdateOriginPricingUseSpecialRatio={
            updateOriginPricingUseSpecialRatio
          }
          onUpdateTargetPricingRatioMode={updateTargetPricingRatioMode}
          t={t}
        />
      ))}
      <div className='mt-3 flex justify-center gap-2'>
        <Select
          size='small'
          filter
          allowCreate
          placeholder={t('选择源分组')}
          optionList={groupOptions}
          value={newSourceGroup || undefined}
          onChange={setNewSourceGroup}
          style={{ width: 200 }}
          position='bottomLeft'
        />
        <Button icon={<IconPlus />} theme='outline' onClick={addSourceGroup}>
          {t('添加分组回退')}
        </Button>
      </div>
    </div>
  );
}
