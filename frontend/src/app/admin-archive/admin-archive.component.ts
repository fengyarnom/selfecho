import { CommonModule } from '@angular/common';
import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { API_BASE } from '../api.config';
import { RouterLink } from '@angular/router';

interface ArchiveItem {
  id: string;
  name: string;
  description?: string;
  createdAt: string;
}

@Component({
  selector: 'app-admin-archive',
  standalone: true,
  imports: [CommonModule, FormsModule, HttpClientModule, RouterLink],
  templateUrl: './admin-archive.component.html',
  styleUrls: ['./admin-archive.component.css']
})
export class AdminArchiveComponent implements OnInit {
  loading = false;
  saving = false;
  error = '';
  archives: ArchiveItem[] = [];
  showMenu = false;

  newName = '';
  newDesc = '';

  editingId: string | null = null;
  editName = '';
  editDesc = '';

  constructor(private http: HttpClient) {}

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading = true;
    this.error = '';
    this.http.get<ArchiveItem[]>(`${API_BASE}/archives`).subscribe({
      next: (data) => {
        this.archives = data ?? [];
        this.loading = false;
      },
      error: () => {
        this.error = '加载归档失败';
        this.loading = false;
      }
    });
  }

  startEdit(item: ArchiveItem): void {
    this.editingId = item.id;
    this.editName = item.name;
    this.editDesc = item.description || '';
  }

  cancelEdit(): void {
    this.editingId = null;
    this.editName = '';
    this.editDesc = '';
  }

  saveEdit(): void {
    if (!this.editingId) return;
    const name = this.editName.trim();
    if (!name) {
      this.error = '名称不能为空';
      return;
    }
    this.saving = true;
    this.http
      .put(`${API_BASE}/archives/${this.editingId}`, { name, description: this.editDesc.trim() }, { responseType: 'text' })
      .subscribe({
        next: () => {
          this.saving = false;
          this.cancelEdit();
          this.load();
        },
        error: (err) => {
          this.saving = false;
          this.error = err?.error?.error || '更新失败';
        }
      });
  }

  create(): void {
    const name = this.newName.trim();
    if (!name) {
      this.error = '名称不能为空';
      return;
    }
    this.saving = true;
    this.http
      .post(`${API_BASE}/archives`, { name, description: this.newDesc.trim() }, { responseType: 'text' })
      .subscribe({
        next: () => {
          this.saving = false;
          this.newName = '';
          this.newDesc = '';
          this.load();
        },
        error: (err) => {
          this.saving = false;
          this.error = err?.error?.error || '创建失败';
        }
      });
  }

  remove(id: string): void {
    if (!confirm('确定删除该归档？相关文章将移出该归档。')) return;
    this.saving = true;
    this.http.delete(`${API_BASE}/archives/${id}`, { responseType: 'text' }).subscribe({
      next: () => {
        this.saving = false;
        this.load();
      },
      error: (err) => {
        this.saving = false;
        this.error = err?.error?.error || '删除失败';
      }
    });
  }
}
