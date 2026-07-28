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

import DOMPurify from 'dompurify';
import { marked } from 'marked';
import { normalizeHttpUrl } from './safeNavigation';

const BLOCKED_TAGS = [
  'base',
  'button',
  'embed',
  'form',
  'input',
  'link',
  'meta',
  'object',
  'script',
  'select',
  'textarea',
];

const RICH_CONTENT_SANITIZE_OPTIONS = {
  ADD_ATTR: [
    'allowfullscreen',
    'loading',
    'referrerpolicy',
    'sandbox',
    'target',
  ],
  ADD_TAGS: ['iframe', 'style'],
  FORBID_ATTR: ['formaction', 'srcdoc'],
  FORBID_TAGS: BLOCKED_TAGS,
  FORCE_BODY: true,
};

const INLINE_CONTENT_SANITIZE_OPTIONS = {
  FORBID_ATTR: ['formaction', 'srcdoc', 'style'],
  FORBID_TAGS: [...BLOCKED_TAGS, 'iframe', 'style'],
};

const RICH_IFRAME_SANDBOX =
  'allow-forms allow-popups allow-popups-to-escape-sandbox allow-presentation allow-scripts';

function hardenSanitizedHtml(html) {
  if (typeof document === 'undefined') {
    return html;
  }

  const template = document.createElement('template');
  template.innerHTML = html;

  template.content.querySelectorAll('a[target="_blank"]').forEach((link) => {
    const rel = new Set(
      (link.getAttribute('rel') || '').split(/\s+/).filter(Boolean),
    );
    rel.add('noopener');
    rel.add('noreferrer');
    link.setAttribute('rel', [...rel].join(' '));
  });

  template.content.querySelectorAll('iframe').forEach((frame) => {
    const safeUrl = normalizeHttpUrl(frame.getAttribute('src'));
    if (!safeUrl) {
      frame.remove();
      return;
    }
    frame.setAttribute('src', safeUrl);
    frame.removeAttribute('srcdoc');
    frame.setAttribute('sandbox', RICH_IFRAME_SANDBOX);
    frame.setAttribute('referrerpolicy', 'no-referrer');
    if (!frame.hasAttribute('loading')) {
      frame.setAttribute('loading', 'lazy');
    }
  });

  template.content
    .querySelectorAll('img, audio, video, source')
    .forEach((element) => {
      element.setAttribute('referrerpolicy', 'no-referrer');
    });

  return template.innerHTML;
}

export function sanitizeHtml(content) {
  const html = DOMPurify.sanitize(
    String(content ?? ''),
    INLINE_CONTENT_SANITIZE_OPTIONS,
  );
  return hardenSanitizedHtml(String(html));
}

export function sanitizeRichHtml(content) {
  const html = DOMPurify.sanitize(
    String(content ?? ''),
    RICH_CONTENT_SANITIZE_OPTIONS,
  );
  return hardenSanitizedHtml(String(html));
}

export function renderSafeMarkdown(content) {
  return sanitizeHtml(marked.parse(String(content ?? '')));
}
