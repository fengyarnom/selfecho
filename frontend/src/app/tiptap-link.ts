import { Mark, markInputRule, markPasteRule, mergeAttributes } from '@tiptap/core';

const markdownLinkFind = /(?<!!)\[([^\]]+)\]\((?:\S+?)(?:\s+"[^"]+")?\)\s?$/;
const markdownLinkPasteFind = /(?<!!)\[([^\]]+)\]\((?:\S+?)(?:\s+"[^"]+")?\)/g;
const markdownLinkParse = /\[[^\]]+\]\((\S+?)(?:\s+"([^"]+)")?\)/;

function parseHrefTitle(fullMatch: string): { href: string; title?: string } | null {
  const parsed = markdownLinkParse.exec(fullMatch);
  if (!parsed) return null;
  const [, href, title] = parsed;
  if (!href) return null;
  return { href, title };
}

export const TiptapLink = Mark.create({
  name: 'link',

  inclusive: false,

  addOptions() {
    return {
      HTMLAttributes: {}
    };
  },

  addAttributes() {
    return {
      href: { default: null },
      title: { default: null }
    };
  },

  parseHTML() {
    return [{ tag: 'a[href]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['a', mergeAttributes(this.options.HTMLAttributes, HTMLAttributes), 0];
  },

  addInputRules() {
    return [
      markInputRule({
        find: markdownLinkFind,
        type: this.type,
        getAttributes: (match) => {
          const parsed = parseHrefTitle(match[0]);
          if (!parsed) return false;
          return { href: parsed.href, title: parsed.title || null };
        }
      })
    ];
  },

  addPasteRules() {
    return [
      markPasteRule({
        find: markdownLinkPasteFind,
        type: this.type,
        getAttributes: (match) => {
          const parsed = parseHrefTitle(match[0]);
          if (!parsed) return false;
          return { href: parsed.href, title: parsed.title || null };
        }
      })
    ];
  }
});
