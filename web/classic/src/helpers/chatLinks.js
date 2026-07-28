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

import { normalizeHttpUrl } from './safeNavigation';

export function parseWebChatPresets(rawConfig) {
  let parsed = rawConfig;
  if (typeof rawConfig === 'string') {
    try {
      parsed = JSON.parse(rawConfig);
    } catch {
      return [];
    }
  }

  if (!Array.isArray(parsed)) return [];

  return parsed
    .map((entry, index) => {
      if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
        return null;
      }

      const values = Object.values(entry);
      if (values.length !== 1 || typeof values[0] !== 'string') {
        return null;
      }

      const template = values[0].trim();
      if (!/^https?:\/\//i.test(template)) {
        return null;
      }

      return { index, template };
    })
    .filter(Boolean);
}

export function resolveWebChatUrl(template, apiKey, serverAddress) {
  if (!template || !apiKey || !serverAddress) return '';

  const normalizedKey = String(apiKey).trim().startsWith('sk-')
    ? String(apiKey).trim()
    : `sk-${String(apiKey).trim()}`;
  const resolved = template
    .replaceAll('{address}', encodeURIComponent(serverAddress))
    .replaceAll('{key}', normalizedKey);

  return normalizeHttpUrl(resolved) || '';
}
