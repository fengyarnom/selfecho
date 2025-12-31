import { Component, OnInit } from '@angular/core';
import { RouterLink, RouterOutlet } from '@angular/router';
import { CommonModule } from '@angular/common';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { API_BASE } from './api.config';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [CommonModule, RouterOutlet, RouterLink, HttpClientModule],
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.css']
})
export class AppComponent implements OnInit {
  menuOpen = false;
  siteTitle = "Yarnom'Blog";

  constructor(private http: HttpClient) {}

  ngOnInit(): void {
    this.fetchSiteConfig();
  }

  toggleMenu(): void {
    this.menuOpen = !this.menuOpen;
  }

  private fetchSiteConfig(): void {
    this.http.get<{ title?: string }>(`${API_BASE}/site`).subscribe({
      next: (data) => {
        if (data?.title) {
          this.siteTitle = data.title;
        }
      },
      error: () => {
        // Keep fallback title on error.
      }
    });
  }
}
