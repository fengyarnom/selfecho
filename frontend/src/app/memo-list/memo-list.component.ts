import { CommonModule } from '@angular/common';
import { Component, OnInit } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { API_BASE } from '../api.config';

interface Memo {
  id: string;
  title: string;
  archive?: string;
  createdAt: string;
  bodyMd: string;
  bodyHtml?: string;
}

@Component({
  selector: 'app-memo-list',
  standalone: true,
  imports: [CommonModule, RouterLink],
  templateUrl: './memo-list.component.html',
  styleUrls: ['./memo-list.component.css']
})
export class MemoListComponent implements OnInit {
  loading = false;
  error = '';
  memos: Memo[] = [];
  page = 1;
  limit = 12;
  total = 0;

  constructor(private http: HttpClient) {}

  ngOnInit(): void {
    this.fetchMemos();
  }

  get totalPages(): number {
    return this.total > 0 ? Math.ceil(this.total / this.limit) : 1;
  }

  nextPage(): void {
    if (this.page < this.totalPages) {
      this.page += 1;
      this.fetchMemos();
    }
  }

  prevPage(): void {
    if (this.page > 1) {
      this.page -= 1;
      this.fetchMemos();
    }
  }

  memoIndex(idx: number): number {
    if (!this.total) {
      return (this.page - 1) * this.limit + idx + 1;
    }
    // 让最新的显示最大序号
    return this.total - ((this.page - 1) * this.limit + idx);
  }

  relativeTime(dateStr: string): string {
    const date = new Date(dateStr);
    const diffMs = Date.now() - date.getTime();
    const minutes = Math.floor(diffMs / (1000 * 60));
    const hours = Math.floor(minutes / 60);
    const days = Math.floor(hours / 24);
    if (minutes < 1) return '刚刚';
    if (minutes < 60) return `${minutes} 分钟前`;
    if (hours < 24) return `${hours} 小时前`;
    if (days < 30) return `${days} 天前`;
    return date.toLocaleDateString();
  }

  private fetchMemos(): void {
    this.loading = true;
    this.error = '';
    this.http
      .get<Memo[]>(`${API_BASE}/articles`, {
        params: {
          page: this.page,
          limit: this.limit,
          status: 'published',
          type: 'memo'
        },
        observe: 'response'
      })
      .subscribe({
        next: (res) => {
          const data = res.body ?? [];
          const totalHeader = res.headers.get('X-Total-Count');
          this.total = totalHeader ? parseInt(totalHeader, 10) || data.length : data.length;
          this.memos = data;
          this.loading = false;
        },
        error: () => {
          this.error = '加载备忘录失败';
          this.loading = false;
        }
      });
  }
}
