import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { API_BASE } from '../api.config';

type Status = 'draft' | 'published';
type ArticleType = 'post' | 'memo';

interface Article {
  id: string;
  type: ArticleType;
  title: string;
  slug: string;
  archive?: string;
  status: Status;
  publishedAt?: string;
  createdAt?: string;
}

@Component({
  selector: 'app-admin',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterLink, HttpClientModule],
  templateUrl: './admin.component.html',
  styleUrls: ['./admin.component.css']
})
export class AdminComponent implements OnInit {
  articles: Article[] = [];
  error = '';
  loading = false;
  saving = false;
  showMenu = false;
  page = 1;
  limit = 10;
  total = 0;
  filterType: ArticleType | 'all' = 'all';
  fixedType: ArticleType | null = null;

  constructor(
    private http: HttpClient,
    private route: ActivatedRoute
  ) {}

  ngOnInit(): void {
    this.route.data.subscribe((data) => {
      const fixed = data?.['fixedType'];
      this.fixedType = fixed === 'post' || fixed === 'memo' ? fixed : null;
      if (this.fixedType) {
        this.filterType = this.fixedType;
      }
      this.page = 1;
      this.loadArticles();
    });
  }

  get pageLabel(): string {
    if (this.fixedType === 'memo') return 'Memos';
    return 'Posts';
  }

  get emptyLabel(): string {
    if (this.fixedType === 'memo') return '暂无备忘录';
    return '暂无文章';
  }

  get newLabel(): string {
    if (this.fixedType === 'memo') return 'New Memo';
    return 'New Post';
  }

  get newTypeQueryParam(): ArticleType | null {
    if (this.fixedType === 'post' || this.fixedType === 'memo') return this.fixedType;
    return this.filterType === 'all' ? null : this.filterType;
  }

  get totalPages(): number {
    return this.total > 0 ? Math.ceil(this.total / this.limit) : 1;
  }

  nextPage(): void {
    if (this.page < this.totalPages) {
      this.page += 1;
      this.loadArticles();
    }
  }

  prevPage(): void {
    if (this.page > 1) {
      this.page -= 1;
      this.loadArticles();
    }
  }

  loadArticles() {
    this.loading = true;
    const typeParam = this.fixedType ? this.fixedType : this.filterType === 'all' ? '' : this.filterType;
    this.http
      .get<Article[]>(`${API_BASE}/articles`, {
        observe: 'response',
        params: {
          page: this.page,
          limit: this.limit,
          type: typeParam,
          compact: '1'
        }
      })
      .subscribe({
        next: (res) => {
          const data = res.body ?? [];
          const totalHeader = res.headers.get('X-Total-Count');
          this.total = totalHeader ? parseInt(totalHeader, 10) || data.length : data.length;
          this.articles = data;
          this.loading = false;
        },
        error: (err) => {
          this.error = err?.error?.error || '加载文章失败';
          this.loading = false;
        }
      });
  }

  remove(id: string) {
    this.saving = true;
    this.http.delete(`${API_BASE}/articles/${id}`, { responseType: 'text' }).subscribe({
      next: () => {
        this.saving = false;
        this.loadArticles();
      },
      error: (err) => {
        this.saving = false;
        this.error = err?.error?.error || '删除失败';
      }
    });
  }
}
