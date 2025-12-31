import { CommonModule } from '@angular/common';
import { Component } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Router, RouterLink } from '@angular/router';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { API_BASE } from '../api.config';

@Component({
  selector: 'app-login',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterLink, HttpClientModule],
  templateUrl: './login.component.html',
  styleUrls: ['./login.component.css']
})
export class LoginComponent {
  username = '';
  password = '';
  loading = false;
  error = '';

  constructor(private http: HttpClient, private router: Router) {}

  submit(): void {
    if (!this.username || !this.password || this.loading) return;
    this.loading = true;
    this.error = '';
    this.http
      .post(
        `${API_BASE}/auth/login`,
        { username: this.username, password: this.password },
        { withCredentials: true }
      )
      .subscribe({
        next: () => {
          this.loading = false;
          this.router.navigate(['/admin/posts']);
        },
        error: (err) => {
          this.loading = false;
          this.error = err?.error?.error || '登录失败，请重试';
        }
      });
  }
}
