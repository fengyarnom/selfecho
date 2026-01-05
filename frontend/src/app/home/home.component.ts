import { CommonModule } from '@angular/common';
import { Component, OnInit } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { marked } from 'marked';
import { API_BASE } from '../api.config';
import * as katex from 'katex';
import { RouterModule } from '@angular/router';

@Component({
  selector: 'app-home',
  standalone: true,
  imports: [CommonModule, RouterModule],
  templateUrl: './home.component.html',
  styleUrls: ['./home.component.css']
})
export class HomeComponent implements OnInit {
  loading = true;
  error = '';
  articles: ArticleView[] = [];
  private fullArticles: ArticleView[] = [];
  page = 1;
  limit = 6;
  total = 0;

  constructor(private http: HttpClient, private sanitizer: DomSanitizer) {}

  ngOnInit(): void {
    this.fetchArticles();
  }

  get totalPages(): number {
    return this.total > 0 ? Math.ceil(this.total / this.limit) : 1;
  }

  nextPage(): void {
    if (this.page < this.totalPages) {
      this.page += 1;
      this.fetchArticles();
    }
  }

  prevPage(): void {
    if (this.page > 1) {
      this.page -= 1;
      this.fetchArticles();
    }
  }

  private fetchArticles(): void {
    this.loading = true;
    this.error = '';
    this.http
      .get<ArticlePayload[]>(`${API_BASE}/articles`, {
        params: { page: this.page, limit: this.limit, status: 'published', type: 'post' },
        observe: 'response'
      })
      .subscribe({
        next: (res) => {
          const data = res.body ?? [];
          const totalHeader = res.headers.get('X-Total-Count');
          this.total = totalHeader ? parseInt(totalHeader, 10) || data.length : data.length;
          this.fullArticles = data.map((item) => ({
            id: item.id,
            title: item.title,
            slug: item.slug,
            createdAt: item.createdAt,
            archive: item.archive || '',
            body: this.toHtml(item.bodyMd, item.bodyHtml)
          }));
          this.stageArticles();
        },
        error: () => {
          this.error = '加载文章失败';
          this.loading = false;
        }
      });
  }

  refreshPage(): void {
    this.page = 1;
    this.fetchArticles();
  }

  private toHtml(md: string | undefined, htmlFromApi?: string): SafeHtml {
    const content = htmlFromApi && htmlFromApi.trim().length > 0 ? htmlFromApi : md || '';
    const parsed =
      htmlFromApi && htmlFromApi.trim().length > 0
        ? content
        : marked.parse(content, { breaks: true });
    const html =
      typeof parsed === 'string'
        ? this.renderMath(this.enhanceImages(parsed))
        : '';
    return this.sanitizer.bypassSecurityTrustHtml(html);
  }

  private stageArticles(): void {
    this.articles = [];
    this.loading = false;
    const source = [...this.fullArticles];
    const BATCH = 6;
    const pushBatch = () => {
      if (source.length === 0) {
        return;
      }
      this.articles = [...this.articles, ...source.splice(0, BATCH)];
      if (source.length > 0) {
        setTimeout(pushBatch, 30);
      }
    };
    pushBatch();
  }

  private enhanceImages(html: string): string {
    return html.replace(/<img\b([^>]*)>/gi, (_match, attrs) => {
      const hasLazy = /\bloading\s*=\s*["']?\w+["']?/i.test(attrs);
      const hasClass = /\bclass\s*=/i.test(attrs);
      const classAttr = hasClass
        ? attrs.replace(/\bclass\s*=\s*["']([^"]*)"/i, (_m: string, cls: string) => `class="${cls} mx-auto"`)
        : `${attrs} class="mx-auto"`;
      const finalAttrs = hasLazy ? classAttr : `${classAttr} loading="lazy"`;
      return `<img ${finalAttrs}>`;
    });
  }

  private renderMath(html: string): string {
    if (!/\$(.+?)\$|\$\$([\s\S]+?)\$\$/.test(html)) {
      return html;
    }
    const render = (expression: string, displayMode: boolean): string => {
      const cleaned = this.decodeEntities(
        expression
          .replace(/<br\s*\/?>/gi, '')
          .replace(/<\/?p>/gi, '')
          .trim()
      );
      try {
        return katex.renderToString(cleaned, { displayMode, throwOnError: false });
      } catch {
        return cleaned;
      }
    };

    // Block math: $$...$$
    const blockMath = /\$\$([\s\S]+?)\$\$/g;
    html = html.replace(blockMath, (_m, expr) => render(expr, true));

    // Inline math: $...$ (simple, non-greedy)
    const inlineMath = /\$(.+?)\$/g;
    html = html.replace(inlineMath, (_m, expr) => render(expr, false));

    return html;
  }

  private decodeEntities(text: string): string {
    const textarea = document.createElement('textarea');
    textarea.innerHTML = text;
    return textarea.value;
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

interface ArticleView {
  id: string;
  title: string;
  slug: string;
  createdAt: string;
  archive?: string;
  body: SafeHtml;
}
