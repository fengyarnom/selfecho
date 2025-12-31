import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink } from '@angular/router';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { API_BASE } from '../api.config';

type Status = 'draft' | 'published';

interface Article {
  id: string;
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
  imports: [CommonModule, RouterLink, HttpClientModule],
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

  constructor(private http: HttpClient) {}

  ngOnInit(): void {
    this.loadArticles();
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
    this.http
      .get<Article[]>(`${API_BASE}/articles`, {
        observe: 'response',
        params: {
          page: this.page,
          limit: this.limit,
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
