import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { marked } from 'marked';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { API_BASE } from '../api.config';

type Status = 'draft' | 'published';

interface ArticlePayload {
  title: string;
  slug: string;
  archive: string;
  status: Status;
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
  form: ArticlePayload = { title: '', slug: '', archive: '', status: 'draft', bodyMd: '' };
  editingId: string | null = null;
  error = '';
  saving = false;
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
}
