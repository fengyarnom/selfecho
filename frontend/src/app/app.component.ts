import { Component, OnInit } from '@angular/core';
import { Router, RouterLink, RouterOutlet } from '@angular/router';
import { CommonModule } from '@angular/common';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { Title } from '@angular/platform-browser';
import { API_BASE } from './api.config';
import { SiteTitleService } from './site-title.service';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [CommonModule, RouterOutlet, RouterLink, HttpClientModule],
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.css']
})
export class AppComponent implements OnInit {
  menuOpen = false;
  siteTitle = 'Selfecho';

  constructor(
    private http: HttpClient,
    private title: Title,
    private router: Router,
    private siteTitleService: SiteTitleService
  ) {}

  ngOnInit(): void {
    const currentTitle = this.title.getTitle();
    if (!currentTitle || currentTitle === this.siteTitle) {
      this.title.setTitle(this.siteTitle);
    }
    this.siteTitleService.setTitle(this.siteTitle);
    this.fetchSiteConfig();
  }

  toggleMenu(): void {
    this.menuOpen = !this.menuOpen;
  }

  private fetchSiteConfig(): void {
    this.http.get<{ title?: string }>(`${API_BASE}/site`).subscribe({
      next: (data) => {
        if (data?.title) {
          const prevSiteTitle = this.siteTitle;
          this.siteTitle = data.title;
          this.siteTitleService.setTitle(this.siteTitle);

          const currentTitle = this.title.getTitle();
          if (currentTitle === prevSiteTitle) {
            this.title.setTitle(this.siteTitle);
          } else {
            const suffix = ` - ${prevSiteTitle}`;
            if (currentTitle.endsWith(suffix)) {
              this.title.setTitle(currentTitle.slice(0, -suffix.length) + ` - ${this.siteTitle}`);
            }
          }
        }
      },
      error: () => {
        // Keep fallback title on error.
      }
    });
  }

  goAdmin(): void {
    this.http.get(`${API_BASE}/auth/me`, { withCredentials: true }).subscribe({
      next: () => this.router.navigate(['/admin/posts']),
      error: () => this.router.navigate(['/login'])
    });
  }
}
