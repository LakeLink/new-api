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

import React, { useEffect, useMemo, useRef } from 'react';
import {
  renderSafeMarkdown,
  sanitizeHtml,
  sanitizeRichHtml,
} from '../../helpers/safeHtml';

const ISOLATED_CONTENT_STYLES = `
  :host {
    display: block;
    width: 100%;
    color: inherit;
    font: inherit;
  }

  *,
  *::before,
  *::after {
    box-sizing: border-box;
  }

  img,
  video,
  iframe {
    max-width: 100%;
  }

  iframe {
    border: 0;
  }
`;

function IsolatedHtml({ className, html, style }) {
  const containerRef = useRef(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return undefined;
    }

    const shadowRoot =
      container.shadowRoot || container.attachShadow({ mode: 'open' });
    const style = document.createElement('style');
    style.textContent = ISOLATED_CONTENT_STYLES;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    shadowRoot.replaceChildren(style, wrapper);

    return () => shadowRoot.replaceChildren();
  }, [html]);

  return <div ref={containerRef} className={className} style={style} />;
}

export default function SafeHtml({
  className,
  content,
  isolated = false,
  markdown = false,
  rich = false,
  style,
}) {
  const html = useMemo(() => {
    if (markdown) {
      return renderSafeMarkdown(content);
    }
    return rich ? sanitizeRichHtml(content) : sanitizeHtml(content);
  }, [content, markdown, rich]);

  if (isolated) {
    return <IsolatedHtml className={className} html={html} style={style} />;
  }

  return (
    <div
      className={className}
      style={style}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}
