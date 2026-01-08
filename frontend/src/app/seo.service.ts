import { Injectable, Inject } from '@angular/core';
import { Meta, Title } from '@angular/platform-browser';
import { DOCUMENT } from '@angular/common';

export type SeoUpdate = {
  title?: string;
  description?: string;
  canonical?: string;
};

@Injectable({ providedIn: 'root' })
export class SeoService {
  constructor(
    private readonly title: Title,
    private readonly meta: Meta,
    @Inject(DOCUMENT) private readonly document: Document
  ) {}

  update(update: SeoUpdate): void {
    const canonical = update.canonical ? this.absoluteURL(update.canonical) : '';
    if (update.title) {
      this.title.setTitle(update.title);
      this.meta.updateTag({ property: 'og:title', content: update.title });
    }
    if (update.description) {
      this.meta.updateTag({ name: 'description', content: update.description });
      this.meta.updateTag({ property: 'og:description', content: update.description });
    }
    if (canonical) {
      this.setCanonical(canonical);
      this.meta.updateTag({ property: 'og:url', content: canonical });
    }
  }

  setCanonical(urlOrPath: string): void {
    const href = this.absoluteURL(urlOrPath);
    const link = this.getOrCreateLink('canonical');
    link.setAttribute('href', href);
  }

  private absoluteURL(urlOrPath: string): string {
    if (/^https?:\/\//i.test(urlOrPath)) return urlOrPath;
    const origin = this.document.defaultView?.location?.origin || '';
    if (!origin) return urlOrPath;
    if (urlOrPath.startsWith('/')) return origin + urlOrPath;
    return origin + '/' + urlOrPath;
  }

  private getOrCreateLink(rel: string): HTMLLinkElement {
    const existing = this.document.querySelector(`link[rel="${rel}"]`) as HTMLLinkElement | null;
    if (existing) return existing;
    const link = this.document.createElement('link');
    link.setAttribute('rel', rel);
    this.document.head.appendChild(link);
    return link;
  }
}
