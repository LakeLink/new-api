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

const BLOCKED_EXTERNAL_PROTOCOLS = new Set([
  'blob:',
  'data:',
  'file:',
  'javascript:',
  'vbscript:',
]);

export function normalizeHttpUrl(value) {
  if (typeof value !== 'string') {
    return null;
  }

  const trimmed = value.trim();
  if (!trimmed) {
    return null;
  }

  try {
    const url = new URL(trimmed);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
      return null;
    }
    if (url.username || url.password) {
      return null;
    }
    return url.toString();
  } catch {
    return null;
  }
}

export function openExternalHttpUrl(value) {
  const url = normalizeHttpUrl(value);
  if (!url) {
    return false;
  }

  window.open(url, '_blank', 'noopener,noreferrer');
  return true;
}

export function normalizeExternalProtocolUrl(value) {
  if (typeof value !== 'string') {
    return null;
  }

  const trimmed = value.trim();
  if (!trimmed || !/^[a-z][a-z\d+.-]*:/i.test(trimmed)) {
    return null;
  }

  try {
    const url = new URL(trimmed);
    if (BLOCKED_EXTERNAL_PROTOCOLS.has(url.protocol.toLowerCase())) {
      return null;
    }
    if (
      (url.protocol === 'http:' || url.protocol === 'https:') &&
      (url.username || url.password)
    ) {
      return null;
    }
    return url.toString();
  } catch {
    return null;
  }
}
