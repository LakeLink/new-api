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

import React, { useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useTokenKeys } from '../../hooks/chat/useTokenKeys';
import {
  parseWebChatPresets,
  resolveWebChatUrl,
} from '../../helpers/chatLinks';

const Chat2Page = () => {
  const { t } = useTranslation();
  const { keys, serverAddress, isLoading } = useTokenKeys();
  const firstWebPreset = useMemo(
    () => parseWebChatPresets(localStorage.getItem('chats'))[0],
    [],
  );
  const redirectLink =
    keys.length > 0 && firstWebPreset
      ? resolveWebChatUrl(firstWebPreset.template, keys[0], serverAddress)
      : '';

  useEffect(() => {
    if (redirectLink) {
      window.location.assign(redirectLink);
    }
  }, [redirectLink]);

  return (
    <div className='mt-[60px] px-2'>
      <h3>{isLoading || redirectLink ? t('正在跳转...') : t('加载失败')}</h3>
    </div>
  );
};

export default Chat2Page;
