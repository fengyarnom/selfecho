import { InputRule, Node, mergeAttributes, nodePasteRule } from '@tiptap/core';

const imageMarkdownRegex = /!\[([^\]]*)\]\((\S+?)(?:\s+"([^"]+)")?\)\s?$/;
const imageMarkdownPasteRegex = /!\[([^\]]*)\]\((\S+?)(?:\s+"([^"]+)")?\)/g;

export const TiptapImage = Node.create({
  name: 'image',

  inline: true,
  group: 'inline',
  draggable: true,

  addOptions() {
    return {
      HTMLAttributes: {}
    };
  },

  addAttributes() {
    return {
      src: { default: null },
      alt: { default: null },
      title: { default: null }
    };
  },

  parseHTML() {
    return [{ tag: 'img[src]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['img', mergeAttributes(this.options.HTMLAttributes, HTMLAttributes)];
  },

  addInputRules() {
    return [
      new InputRule({
        find: imageMarkdownRegex,
        handler: ({ range, match, commands }) => {
          const [, alt, src, title] = match as unknown as [string, string, string, string];
          if (!src) return null;

          commands.insertContentAt(
            range,
            {
              type: this.name,
              attrs: {
                src,
                alt: alt || null,
                title: title || null
              }
            },
            { updateSelection: true }
          );
          return;
        }
      })
    ];
  },

  addPasteRules() {
    return [
      nodePasteRule({
        find: imageMarkdownPasteRegex,
        type: this.type,
        getAttributes: (match) => {
          const [, alt, src, title] = match as unknown as [string, string, string, string];
          if (!src) return false;
          return {
            src,
            alt: alt || null,
            title: title || null
          };
        }
      })
    ];
  }
});
