import { CommonModule } from '@angular/common';
import { Component, DestroyRef, OnInit } from '@angular/core';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { API_BASE } from '../api.config';
import { RouterModule } from '@angular/router';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { SeoService } from '../seo.service';
import { SiteTitleService } from '../site-title.service';

interface CategoryItem {
  name: string;
  count: number;
}

@Component({
  selector: 'app-categories',
  standalone: true,
  imports: [CommonModule, HttpClientModule, RouterModule],
  templateUrl: './categories.component.html',
  styleUrls: ['./categories.component.css']
})
export class CategoriesComponent implements OnInit {
  loading = true;
  error = '';
  categories: CategoryItem[] = [];

  constructor(
    private http: HttpClient,
    private seo: SeoService,
    private siteTitle: SiteTitleService,
    private destroyRef: DestroyRef
  ) {}

  ngOnInit(): void {
    this.siteTitle.title$
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((baseTitle) =>
        this.seo.update({
          title: `分类 - ${baseTitle || 'Selfecho'}`,
          description: '分类列表',
          canonical: '/categories'
        })
      );
    this.load();
  }

  private load(): void {
    this.loading = true;
    this.error = '';
    this.http.get<CategoryItem[]>(`${API_BASE}/categories`).subscribe({
      next: (items) => {
        this.categories = items ?? [];
        this.loading = false;
      },
      error: () => {
        this.error = '加载分类失败';
        this.loading = false;
      }
    });
  }
}
