import { CommonModule } from '@angular/common';
import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { HttpClient, HttpClientModule, HttpResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { API_BASE } from '../api.config';

interface ImapAccount {
  id: string;
  host: string;
  port: number;
  username: string;
  useSsl: boolean;
  useStartTls: boolean;
  createdAt: string;
}

interface ImapMessage {
  uid: number;
  subject: string;
  from: string;
  date: string;
  flags: string[];
  snippet: string;
}

interface ImapMessageDetail extends ImapMessage {
  body?: string;
}

@Component({
  selector: 'app-admin-email',
  standalone: true,
  imports: [CommonModule, FormsModule, HttpClientModule, RouterLink],
  templateUrl: './admin-email.component.html',
  styleUrls: ['./admin-email.component.css']
})
export class AdminEmailComponent implements OnInit {
  accounts: ImapAccount[] = [];
  messages: ImapMessage[] = [];
  selected: ImapMessageDetail | null = null;
  selectedAccountId: string | undefined;
  loading = false;
  saving = false;
  error = '';
  showMenu = false;
  showForm = false;
  view: 'email' | 'imap' = 'email';
  page = 1;
  pageSize = 12;
  total = 0;

  form = {
    host: '',
    port: 993,
    username: '',
    password: '',
    useSsl: true,
    useStartTls: false
  };

  constructor(private http: HttpClient) {}

  ngOnInit(): void {
    this.loadAccounts();
  }

  loadAccounts(): void {
    this.http.get<ImapAccount[]>(`${API_BASE}/imap/accounts`).subscribe({
      next: (data) => {
        this.accounts = data || [];
        if (!this.selectedAccountId && this.accounts.length) {
          this.selectedAccountId = this.accounts[0].id;
          this.loadMessages(this.selectedAccountId);
        }
      },
      error: (err) => {
        this.error = err?.error?.error || '加载账号失败';
      }
    });
  }

  saveAccount(): void {
    if (!this.form.host || !this.form.username || !this.form.password || this.saving) return;
    this.saving = true;
    this.error = '';
    this.http.post(`${API_BASE}/imap/accounts`, this.form).subscribe({
      next: () => {
        this.saving = false;
        this.form.password = '';
        this.loadAccounts();
      },
      error: (err) => {
        this.saving = false;
        this.error = err?.error?.error || '保存失败';
      }
    });
  }

  loadMessages(accountId?: string): void {
    if (this.loading) return;
    this.loading = true;
    this.error = '';
    if (accountId && accountId !== this.selectedAccountId) {
      this.page = 1;
      this.selected = null;
    }
    const params: any = {
      limit: this.pageSize,
      page: this.page
    };
    const targetId = accountId || this.selectedAccountId || (this.accounts.length ? this.accounts[0].id : undefined);
    if (targetId) {
      params.accountId = targetId;
      this.selectedAccountId = targetId;
    }
    this.http
      .get<ImapMessage[]>(`${API_BASE}/imap/messages`, {
        params,
        observe: 'response',
        withCredentials: true
      })
      .subscribe({
        next: (res: HttpResponse<ImapMessage[]>) => {
          this.messages = res.body || [];
          const totalHeader = res.headers.get('X-Total-Count');
          this.total = totalHeader ? parseInt(totalHeader, 10) || 0 : this.messages.length;
          this.selected = null;
          this.loading = false;
        },
        error: (err) => {
          this.error = err?.error?.error || '加载邮件失败';
          this.loading = false;
        }
      });
  }

  openMessage(uid: number, accountId?: string): void {
    this.selected = null;
    const params: any = {};
    const targetId = accountId || this.selectedAccountId || (this.accounts.length ? this.accounts[0].id : undefined);
    if (targetId) {
      params.accountId = targetId;
      this.selectedAccountId = targetId;
    }
    this.http
      .get<ImapMessageDetail>(`${API_BASE}/imap/messages/${uid}`, {
        params,
        withCredentials: true
      })
      .subscribe({
        next: (data) => {
          this.selected = data || null;
        },
        error: (err) => {
          this.error = err?.error?.error || '加载邮件失败';
        }
      });
  }

  setView(mode: 'email' | 'imap'): void {
    this.view = mode;
    if (mode === 'email' && !this.messages.length && !this.loading) {
      this.loadMessages();
    }
  }

  get currentAccount(): ImapAccount | undefined {
    return this.accounts.find((a) => a.id === this.selectedAccountId);
  }

  get totalPages(): number {
    return Math.max(1, Math.ceil(this.total / this.pageSize));
  }

  prevPage(): void {
    if (this.page > 1) {
      this.page -= 1;
      this.loadMessages();
    }
  }

  nextPage(): void {
    if (this.page < this.totalPages) {
      this.page += 1;
      this.loadMessages();
    }
  }
}
