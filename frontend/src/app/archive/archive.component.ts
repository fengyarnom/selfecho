import { CommonModule } from '@angular/common';
import { Component, OnInit } from '@angular/core';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { ActivatedRoute, RouterModule } from '@angular/router';
import { combineLatest } from 'rxjs';
import { API_BASE } from '../api.config';

interface ArticlePayload {
  id: string;
  title: string;
  slug: string;
  createdAt: string;
  status: string;
  archive?: string;
}

@Component({
  selector: 'app-archive',
  standalone: true,
  imports: [CommonModule, HttpClientModule, RouterModule],
  templateUrl: './archive.component.html',
  styleUrls: ['./archive.component.css']
})
export class ArchiveComponent implements OnInit {
  loading = true;
  error = '';
  articles: ArticlePayload[] = [];
  private full: ArticlePayload[] = [];

  selectedArchive = '';

  constructor(private http: HttpClient, private route: ActivatedRoute) {}

  ngOnInit(): void {
    combineLatest([this.route.paramMap, this.route.queryParamMap]).subscribe(([params, query]) => {
      this.selectedArchive = params.get('name') || query.get('archive') || '';
      this.load();
    });
  }

  private load(): void {
    this.loading = true;
    this.error = '';
    this.http
      .get<ArticlePayload[]>(`${API_BASE}/articles`, {
        params: {
          archive: this.selectedArchive || '',
          status: 'published',
          type: 'post',
          compact: '1'
        }
      })
      .subscribe({
        next: (data) => {
          const filtered = (data ?? []).filter((a) => a.status === 'published');
          const target = this.selectedArchive === '未分类' ? '' : this.selectedArchive;
          this.full = this.selectedArchive
            ? filtered.filter((a) => (a.archive || '') === target)
            : filtered;
          this.stage();
        },
        error: () => {
          this.error = '加载归档失败';
          this.loading = false;
        }
      });
  }

  private stage(): void {
    this.articles = [];
    this.loading = false;
    const source = [...this.full];
    const BATCH = 8;
    const pushBatch = () => {
      if (source.length === 0) return;
      this.articles = [...this.articles, ...source.splice(0, BATCH)];
      if (source.length > 0) {
        setTimeout(pushBatch, 30);
      }
    };
    pushBatch();
  }
}
