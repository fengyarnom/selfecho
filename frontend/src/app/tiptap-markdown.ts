import type { JSONContent } from '@tiptap/core';

function escapeText(text: string): string {
  return text.replace(/([\\`*_~[\]])/g, '\\$1');
}

function serializeText(node: JSONContent): string {
  const text = node.text ?? '';
  const marks = node.marks ?? [];
  if (marks.some((m) => m.type === 'code')) {
    const safe = text.replace(/`/g, '\\`');
    return '`' + safe + '`';
  }

  let out = escapeText(text);

  if (marks.some((m) => m.type === 'strike')) out = `~~${out}~~`;
  if (marks.some((m) => m.type === 'bold')) out = `**${out}**`;
  if (marks.some((m) => m.type === 'italic')) out = `*${out}*`;

  const link = marks.find((m) => m.type === 'link');
  const href = link?.attrs?.['href'];
  if (href) {
    const title = (link?.attrs?.['title'] ?? '').toString().trim();
    const titlePart = title ? ` "${title.replace(/"/g, '\\"')}"` : '';
    out = `[${out}](${String(href)}${titlePart})`;
  }

  return out;
}

function serializeInline(nodes: JSONContent[] | undefined): string {
  if (!nodes?.length) return '';
  let out = '';
  for (const n of nodes) {
    if (n.type === 'text') out += serializeText(n);
    else if (n.type === 'hardBreak') out += '  \n';
    else out += serializeNode(n).trimEnd();
  }
  return out;
}

function normalizeBlock(block: string): string {
  return block.replace(/[ \t]+\n/g, '\n').replace(/\n{3,}/g, '\n\n').trim();
}

function serializeListItem(item: JSONContent, prefix: string): string {
  const parts: string[] = [];
  for (const child of item.content ?? []) {
    if (child.type === 'paragraph') {
      parts.push(serializeInline(child.content));
      continue;
    }
    if (child.type === 'bulletList' || child.type === 'orderedList') {
      const nested = serializeNode(child);
      const indented = nested
        .split('\n')
        .map((l) => (l ? '  ' + l : l))
        .join('\n');
      parts.push(indented);
      continue;
    }
    parts.push(serializeNode(child));
  }

  const first = parts.shift() ?? '';
  const rest = parts.length ? '\n' + parts.join('\n') : '';
  return `${prefix}${first}${rest}`.trimEnd();
}

export function tiptapToMarkdown(doc: JSONContent | null | undefined): string {
  if (!doc) return '';
  const blocks: string[] = [];
  for (const n of doc.content ?? []) {
    const s = serializeNode(n);
    if (s) blocks.push(s);
  }
  return normalizeBlock(blocks.join('\n\n')) + '\n';
}

function serializeNode(node: JSONContent): string {
  switch (node.type) {
    case 'paragraph':
      return serializeInline(node.content);
    case 'heading': {
      const level = Number(node.attrs?.['level'] ?? 1);
      const prefix = '#'.repeat(Math.min(Math.max(level, 1), 6));
      return `${prefix} ${serializeInline(node.content)}`.trimEnd();
    }
    case 'bulletList': {
      const items = (node.content ?? []).filter((c) => c.type === 'listItem');
      return items.map((it) => serializeListItem(it, '- ')).join('\n');
    }
    case 'orderedList': {
      const items = (node.content ?? []).filter((c) => c.type === 'listItem');
      let i = Number(node.attrs?.['start'] ?? 1);
      return items.map((it) => serializeListItem(it, `${i++}. `)).join('\n');
    }
    case 'blockquote': {
      const inner = (node.content ?? []).map(serializeNode).join('\n\n');
      return inner
        .split('\n')
        .map((l) => (l ? `> ${l}` : '>'))
        .join('\n');
    }
    case 'codeBlock': {
      const lang = (node.attrs?.['language'] ?? '').toString().trim();
      const code = (node.content ?? [])
        .filter((c) => c.type === 'text')
        .map((t) => t.text ?? '')
        .join('');
      return `\`\`\`${lang}\n${code}\n\`\`\``;
    }
    case 'image': {
      const src = (node.attrs?.['src'] ?? '').toString().trim();
      if (!src) return '';
      const alt = (node.attrs?.['alt'] ?? '').toString();
      const title = (node.attrs?.['title'] ?? '').toString().trim();
      const titlePart = title ? ` "${title.replace(/"/g, '\\"')}"` : '';
      return `![${alt.replace(/]/g, '\\]')}](${src}${titlePart})`;
    }
    case 'horizontalRule':
      return '---';
    case 'text':
      return serializeText(node);
    default:
      return serializeInline(node.content);
  }
}
