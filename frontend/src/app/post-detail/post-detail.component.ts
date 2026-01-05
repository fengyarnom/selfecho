import { CommonModule } from '@angular/common';
import { Component, OnInit } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { HttpClient } from '@angular/common/http';
import { API_BASE } from '../api.config';

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
export class PostDetailComponent implements OnInit {
  loading = true;
  error = '';
  article?: { title: string; createdAt: string; archive?: string; body: SafeHtml };

  constructor(
    private route: ActivatedRoute,
    private http: HttpClient,
    private sanitizer: DomSanitizer
  ) {}

  ngOnInit(): void {
    const slug = this.route.snapshot.paramMap.get('slug');
    if (!slug) {
      this.error = '未找到该文章';
      this.loading = false;
      return;
    }
    this.fetchArticle(slug);
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
            this.error = '文章不存在或未发布';
            this.loading = false;
            return;
          }
          const html = (item.bodyHtml && item.bodyHtml.trim().length > 0 ? item.bodyHtml : item.bodyMd) || '';
          this.article = {
            title: item.title,
            createdAt: item.createdAt,
            archive: item.archive || '',
            body: this.sanitizer.bypassSecurityTrustHtml(html)
          };
          this.loading = false;
        },
        error: () => {
          this.error = '加载文章失败';
          this.loading = false;
        }
      });
  }
}
