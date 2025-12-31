import { CommonModule } from '@angular/common';
import { Component, OnInit } from '@angular/core';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { API_BASE } from '../api.config';
import { RouterLink } from '@angular/router';

interface HealthPayload {
  cpuPercent: number;
  totalMemBytes: number;
  usedMemBytes: number;
  diskTotalBytes: number;
  diskUsedBytes: number;
  processRssBytes?: number;
  processVmsBytes?: number;
  processOpenFds?: number;
  dbOpen?: number;
  dbIdle?: number;
  dbInUse?: number;
  goVersion?: string;
  binarySizeBytes?: number;
  goroutines?: number;
  uptimeSeconds?: number;
  dbLatencyMs?: number;
  cacheEntries?: number;
  cacheHits?: number;
  cacheMisses?: number;
  cacheHitRate?: number;
  cacheTtlSeconds?: number;
}

@Component({
  selector: 'app-admin-health',
  standalone: true,
  imports: [CommonModule, HttpClientModule, RouterLink],
  templateUrl: './admin-health.component.html',
  styleUrls: ['./admin-health.component.css']
})
export class AdminHealthComponent implements OnInit {
  loading = true;
  error = '';
  data: HealthPayload | null = null;
  showMenu = false;

  constructor(private http: HttpClient) {}

  ngOnInit(): void {
    this.fetch();
  }

  fetch(): void {
    this.loading = true;
    this.error = '';
    this.http.get<HealthPayload>(`${API_BASE}/health`).subscribe({
      next: (d) => {
        this.data = d;
        this.loading = false;
      },
      error: () => {
        this.error = '加载健康数据失败';
        this.loading = false;
      }
    });
  }

  formatBytes(val?: number): string {
    if (!val && val !== 0) return '-';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let v = val;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return `${v.toFixed(1)} ${units[i]}`;
  }

  formatSeconds(sec?: number): string {
    if (sec === undefined || sec === null) return '-';
    const s = Math.max(0, Math.floor(sec));
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    const rem = s % 60;
    const parts = [];
    if (d) parts.push(`${d}d`);
    if (h) parts.push(`${h}h`);
    if (m) parts.push(`${m}m`);
    if (rem || parts.length === 0) parts.push(`${rem}s`);
    return parts.join(' ');
  }
}
