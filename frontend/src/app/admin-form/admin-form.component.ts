import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { marked } from 'marked';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { API_BASE } from '../api.config';

type Status = 'draft' | 'published';
type ArticleType = 'post' | 'memo';

interface ArticlePayload {
  title: string;
  slug: string;
  archive: string;
  status: Status;
  type: ArticleType;
  bodyMd: string;
}

interface Article extends ArticlePayload {
  id: string;
}

@Component({
  selector: 'app-admin-form',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterLink, HttpClientModule],
  templateUrl: './admin-form.component.html',
  styleUrls: ['./admin-form.component.css']
})
export class AdminFormComponent implements OnInit {
  form: ArticlePayload = { title: '', slug: '', archive: '', status: 'draft', type: 'post', bodyMd: '' };
  editingId: string | null = null;
  error = '';
  saving = false;
  slugGenerating = false;
  slugMessage = '';
  archives: string[] = [];
  selectedArchive = '';

  constructor(
    private route: ActivatedRoute,
    private router: Router,
    private http: HttpClient,
    private sanitizer: DomSanitizer
  ) {}

  ngOnInit(): void {
    const id = this.route.snapshot.paramMap.get('id');
    if (id) {
      this.editingId = id;
      this.loadArticle(id);
    }
    this.loadArchives();
  }

  loadArticle(id: string) {
    this.http.get<Article[]>(`${API_BASE}/articles`).subscribe({
      next: (list) => {
        const found = list.find((a) => a.id === id);
        if (!found) {
          this.error = '未找到文章';
          return;
        }
        this.form = {
          title: found.title,
          slug: found.slug,
          archive: found.archive || '',
          status: found.status,
          type: (found as any).type || 'post',
          bodyMd: found.bodyMd || ''
        };
      },
      error: () => {
        this.error = '加载文章失败';
      }
    });
  }

  save() {
    this.error = '';
    if (!this.form.title.trim()) {
      this.error = '标题必填';
      return;
    }
    this.saving = true;

    if (this.editingId) {
      this.http
        .put(`${API_BASE}/articles/${this.editingId}`, this.form, { responseType: 'text' })
        .subscribe({
          next: () => this.afterSave(),
          error: (err: any) => this.fail(err, '保存失败')
        });
    } else {
      this.http.post<{ id: string }>(`${API_BASE}/articles`, this.form).subscribe({
        next: () => this.afterSave(),
        error: (err: any) => this.fail(err, '保存失败')
      });
    }
  }

  private afterSave() {
    this.saving = false;
    this.router.navigate(['/admin']);
  }

  private fail(err: any, msg: string) {
    this.saving = false;
    this.error = err?.error?.error || msg;
  }

  private loadArchives() {
    this.http.get<{ name: string; count: number }[]>(`${API_BASE}/categories`).subscribe({
      next: (items) => {
        this.archives = (items ?? []).map((i) => i.name);
      },
      error: () => {
        // ignore
      }
    });
  }

  onArchiveChange(event: Event) {
    const target = event.target as HTMLSelectElement | null;
    if (!target) {
      return;
    }
    const val = target.value;
    if (!val) {
      return;
    }
    this.form.archive = val;
  }

  generateSlug(mode: 'llm' | 'pinyin') {
    if (!this.form.title.trim()) {
      this.error = '请先填写标题';
      return;
    }
    this.slugGenerating = true;
    this.slugMessage = '';
    this.error = '';
    this.http
      .post<{ slug: string; source: string }>(
        `${API_BASE}/slug`,
        { title: this.form.title, mode },
        { withCredentials: true }
      )
      .subscribe({
        next: (res) => {
          this.form.slug = res.slug;
          this.slugMessage = res.source === 'llm' ? '已使用 DeepSeek 生成' : '已使用拼音生成';
          this.slugGenerating = false;
        },
        error: (err: any) => {
          this.slugGenerating = false;
          this.error = err?.error?.error || '生成 slug 失败';
        }
      });
  }
}
