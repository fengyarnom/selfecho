declare module 'katex' {
  export interface KatexOptions {
    displayMode?: boolean;
    throwOnError?: boolean;
    [key: string]: any;
  }
  export function renderToString(expr: string, options?: KatexOptions): string;
}
