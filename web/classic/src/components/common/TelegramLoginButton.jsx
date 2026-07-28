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

import PropTypes from 'prop-types';
import React, { useEffect, useRef } from 'react';
import { SiTelegram } from 'react-icons/si';

const TELEGRAM_WIDGET_SRC = 'https://telegram.org/js/telegram-widget.js?22';
let telegramCallbackSequence = 0;

const TelegramLoginButton = ({
  botName,
  className,
  dataOnauth,
  disabled = false,
  disabledLabel = 'Telegram',
  onDisabledClick,
}) => {
  const containerRef = useRef(null);
  const callbackRef = useRef(dataOnauth);
  const callbackNameRef = useRef('');

  if (!callbackNameRef.current) {
    telegramCallbackSequence += 1;
    callbackNameRef.current = `__newApiClassicTelegramAuth${telegramCallbackSequence}`;
  }

  useEffect(() => {
    callbackRef.current = dataOnauth;
  }, [dataOnauth]);

  useEffect(() => {
    if (disabled) return undefined;

    const normalizedBotName =
      typeof botName === 'string' ? botName.trim().replace(/^@/, '') : '';
    if (!/^[a-z\d_]{5,32}$/i.test(normalizedBotName)) return undefined;

    const container = containerRef.current;
    if (!container) return undefined;

    const callbackName = callbackNameRef.current;
    const handleAuthorization = (user) => {
      callbackRef.current(user);
    };
    window[callbackName] = handleAuthorization;

    const script = document.createElement('script');
    script.src = TELEGRAM_WIDGET_SRC;
    script.async = true;
    script.referrerPolicy = 'origin';
    script.setAttribute('data-telegram-login', normalizedBotName);
    script.setAttribute('data-size', 'large');
    script.setAttribute('data-userpic', 'true');
    script.setAttribute('data-onauth', `window.${callbackName}(user)`);
    container.replaceChildren(script);

    return () => {
      container.replaceChildren();
      if (window[callbackName] === handleAuthorization) {
        delete window[callbackName];
      }
    };
  }, [botName, disabled]);

  if (disabled) {
    return (
      <button
        type='button'
        className={`inline-flex min-h-[40px] min-w-[220px] items-center justify-center gap-2 rounded bg-[#54a9eb] px-5 py-2 text-sm font-medium text-white opacity-60 ${className || ''}`.trim()}
        aria-disabled='true'
        onClick={onDisabledClick}
      >
        <SiTelegram aria-hidden='true' size={20} />
        {disabledLabel}
      </button>
    );
  }

  return (
    <div
      ref={containerRef}
      className={`min-h-[40px] ${className || ''}`.trim()}
      aria-label='Telegram'
    />
  );
};

TelegramLoginButton.propTypes = {
  botName: PropTypes.string.isRequired,
  className: PropTypes.string,
  dataOnauth: PropTypes.func.isRequired,
  disabled: PropTypes.bool,
  disabledLabel: PropTypes.string,
  onDisabledClick: PropTypes.func,
};

export default TelegramLoginButton;
