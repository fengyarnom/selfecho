import { CommonModule } from '@angular/common';
import { Component, DestroyRef, OnDestroy, OnInit } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { HttpClient } from '@angular/common/http';
import { API_BASE } from '../api.config';
import { SeoService } from '../seo.service';
import { SiteTitleService } from '../site-title.service';
import { marked } from 'marked';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';

declare global {
  interface Window {
    remark_config?: {
      host: string;
      site_id: string;
      url: string;
    };
    REMARK42?: any;
  }
}

interface ArticlePayload {
  id: string;
  title: string;
  slug: string;
  createdAt: string;
  archive?: string;
  bodyMd: string;
  bodyHtml?: string;
}

@Component({
  selector: 'app-post-detail',
  standalone: true,
  imports: [CommonModule, RouterLink],
  templateUrl: './post-detail.component.html',
  styleUrls: ['./post-detail.component.css']
})
export class PostDetailComponent implements OnInit, OnDestroy {
  loading = true;
  error = '';
  article?: { title: string; createdAt: string; archive?: string; body: SafeHtml };
  private slug = '';
  private baseTitle = this.siteTitle.title || 'Selfecho';
  private excerptSource = '';
  private readonly remarkHost = 'https://comments.yarnom.com';
  private readonly remarkSiteId = 'yarnom';
  private readonly remarkScriptId = 'remark42-embed-script';

  constructor(
    private route: ActivatedRoute,
    private http: HttpClient,
    private sanitizer: DomSanitizer,
    private seo: SeoService,
    private siteTitle: SiteTitleService,
    private destroyRef: DestroyRef
  ) {}

  ngOnInit(): void {
    this.siteTitle.title$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((t) => {
      this.baseTitle = t || 'Selfecho';
      this.refreshHead();
    });

    const slugParam = this.route.snapshot.paramMap.get('slug');
    if (!slugParam) {
      this.error = '未找到该文章';
      this.refreshHead();
      this.loading = false;
      return;
    }
    this.slug = slugParam;
    this.fetchArticle(this.slug);
  }

  ngOnDestroy(): void {
    this.unmountRemark42();
  }

  private fetchArticle(slug: string): void {
    this.loading = true;
    this.error = '';
    this.http
      .get<ArticlePayload[]>(`${API_BASE}/articles`, {
        params: { slug, status: 'published', type: 'post', limit: 1 },
        observe: 'response'
      })
      .subscribe({
        next: (res) => {
          const list = res.body ?? [];
          const item = list[0];
          if (!item) {
            this.unmountRemark42();
            this.article = undefined;
            this.error = '文章不存在或未发布';
            const baseTitle = this.siteTitle.title || 'Selfecho';
            this.seo.update({
              title: `文章不存在 - ${baseTitle}`,
              description: '文章不存在或未发布',
              canonical: '/post/' + encodeURIComponent(slug)
            });
            this.loading = false;
            return;
          }
          const rawMd = item.bodyMd || '';
          const rawHtml = item.bodyHtml || '';
          const renderedHtml =
            rawHtml.trim().length > 0 ? rawHtml : (marked.parse(rawMd, { breaks: true }) as string);
          this.excerptSource = renderedHtml || rawMd;
          this.article = {
            title: item.title,
            createdAt: item.createdAt,
            archive: item.archive || '',
            body: this.sanitizer.bypassSecurityTrustHtml(renderedHtml)
          };
          this.refreshHead();
          this.loading = false;
          setTimeout(() => this.mountRemark42(), 0);
        },
        error: () => {
          this.unmountRemark42();
          this.article = undefined;
          this.error = '加载文章失败';
          this.refreshHead();
          this.loading = false;
        }
      });
  }

  private refreshHead(): void {
    if (!this.slug) {
      this.seo.update({ title: `文章 - ${this.baseTitle}`, canonical: '/post' });
      return;
    }
    if (this.article) {
      this.seo.update({
        title: `${this.article.title} - ${this.baseTitle}`,
        description: this.excerpt(this.excerptSource, 160),
        canonical: '/post/' + encodeURIComponent(this.slug)
      });
      return;
    }
    if (this.error) {
      this.seo.update({
        title: `${this.error} - ${this.baseTitle}`,
        description: this.error,
        canonical: '/post/' + encodeURIComponent(this.slug)
      });
      return;
    }
    this.seo.update({
      title: `加载中 - ${this.baseTitle}`,
      description: '加载文章中',
      canonical: '/post/' + encodeURIComponent(this.slug)
    });
  }

  private excerpt(content: string, maxLen: number): string {
    const text = this.collapseWhitespace(this.stripMarkup(content));
    if (text.length <= maxLen) return text;
    return text.slice(0, Math.max(0, maxLen - 1)) + '…';
  }

  private stripMarkup(input: string): string {
    const withoutTags = input.replace(/<[^>]*>/g, ' ');
    return withoutTags
      .replace(/!\[([^\]]*)\]\([^)]+\)/g, '$1')
      .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
      .replace(/[`*_>#-]/g, ' ');
  }

  private collapseWhitespace(input: string): string {
    return input.replace(/\s+/g, ' ').trim();
  }

  private remarkUrl(): string {
    if (typeof window === 'undefined') return '';
    return window.location.origin + window.location.pathname;
  }

  private mountRemark42(): void {
    if (typeof window === 'undefined' || typeof document === 'undefined') return;
    if (!document.getElementById('remark42')) return;

    window.remark_config = {
      host: this.remarkHost,
      site_id: this.remarkSiteId,
      url: this.remarkUrl()
    };

    try {
      window.REMARK42?.destroy?.();
    } catch {
      // ignore
    }

    const old = document.getElementById(this.remarkScriptId);
    if (old) old.remove();
    try {
      delete window.REMARK42;
    } catch {
      // ignore
    }

    const script = document.createElement('script');
    script.id = this.remarkScriptId;
    script.src = `${this.remarkHost}/web/embed.js`;
    script.defer = true;
    document.body.appendChild(script);
  }

  private unmountRemark42(): void {
    if (typeof window === 'undefined' || typeof document === 'undefined') return;
    try {
      window.REMARK42?.destroy?.();
    } catch {
      // ignore
    }
    const old = document.getElementById(this.remarkScriptId);
    if (old) old.remove();
  }
}
