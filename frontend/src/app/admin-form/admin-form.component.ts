import { AfterViewInit, Component, ElementRef, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { HttpClient, HttpClientModule } from '@angular/common/http';
import { marked } from 'marked';
import { API_BASE } from '../api.config';
import { Editor } from '@tiptap/core';
import StarterKit from '@tiptap/starter-kit';
import { tiptapToMarkdown } from '../tiptap-markdown';
import { TiptapImage } from '../tiptap-image';
import { TiptapLink } from '../tiptap-link';

type Status = 'draft' | 'published';
type ArticleType = 'post' | 'memo';

interface ArticlePayload {
  title: string;
  slug: string;
  archive: string;
  status: Status;
  type: ArticleType;
  bodyMd: string;
  bodyHtml?: string;
}

interface Article extends ArticlePayload {
  id: string;
}

@Component({
  selector: 'app-admin-form',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterLink, HttpClientModule],
  templateUrl: './admin-form.component.html',
  styleUrls: ['./admin-form.component.css']
})
export class AdminFormComponent implements OnInit, AfterViewInit, OnDestroy {
  form: ArticlePayload = {
    title: '',
    slug: '',
    archive: '',
    status: 'draft',
    type: 'post',
    bodyMd: '',
    bodyHtml: ''
  };
  editingId: string | null = null;
  error = '';
  saving = false;
  slugGenerating = false;
  slugMessage = '';
  archives: string[] = [];
  selectedArchive = '';

  @ViewChild('editorHost') editorHost?: ElementRef<HTMLDivElement>;
  editor: Editor | null = null;
  editorView: 'rich' | 'source' | 'preview' = 'rich';
  markdownHtml = '';

  constructor(
    private route: ActivatedRoute,
    private router: Router,
    private http: HttpClient
  ) {}

  ngOnInit(): void {
    const id = this.route.snapshot.paramMap.get('id');
    if (id) {
      this.editingId = id;
      this.loadArticle(id);
    } else {
      const t = this.route.snapshot.queryParamMap.get('type');
      if (t === 'post' || t === 'memo') {
        this.form.type = t;
      }
    }
    this.loadArchives();
  }

  ngAfterViewInit(): void {
    if (!this.editorHost?.nativeElement) return;
    this.editor = new Editor({
      element: this.editorHost.nativeElement,
      extensions: [StarterKit, TiptapImage, TiptapLink],
      content: this.initialEditorHTML(),
      onUpdate: ({ editor }) => {
        this.form.bodyHtml = editor.getHTML();
        this.form.bodyMd = tiptapToMarkdown(editor.getJSON());
        this.markdownHtml = this.renderMarkdown(this.form.bodyMd);
      }
    });
    this.form.bodyHtml = this.editor.getHTML();
    this.form.bodyMd = tiptapToMarkdown(this.editor.getJSON());
    this.markdownHtml = this.renderMarkdown(this.form.bodyMd);
  }

  ngOnDestroy(): void {
    this.editor?.destroy();
    this.editor = null;
  }

  loadArticle(id: string) {
    this.http.get<Article[]>(`${API_BASE}/articles`).subscribe({
      next: (list) => {
        const found = list.find((a) => a.id === id);
        if (!found) {
          this.error = '未找到文章';
          return;
        }
        this.form = {
          title: found.title,
          slug: found.slug,
          archive: found.archive || '',
          status: found.status,
          type: (found as any).type || 'post',
          bodyMd: found.bodyMd || '',
          bodyHtml: (found as any).bodyHtml || ''
        };
        this.syncEditorFromForm();
      },
      error: () => {
        this.error = '加载文章失败';
      }
    });
  }

  save() {
    this.error = '';
    if (!this.form.title.trim()) {
      this.error = '标题必填';
      return;
    }
    this.saving = true;

    if (this.editingId) {
      this.http
        .put(`${API_BASE}/articles/${this.editingId}`, this.form, { responseType: 'text' })
        .subscribe({
          next: () => this.afterSave(),
          error: (err: any) => this.fail(err, '保存失败')
        });
    } else {
      this.http.post<{ id: string }>(`${API_BASE}/articles`, this.form).subscribe({
        next: () => this.afterSave(),
        error: (err: any) => this.fail(err, '保存失败')
      });
    }
  }

  private afterSave() {
    this.saving = false;
    this.router.navigate(['/admin']);
  }

  private fail(err: any, msg: string) {
    this.saving = false;
    this.error = err?.error?.error || msg;
  }

  private loadArchives() {
    this.http.get<{ name: string; count: number }[]>(`${API_BASE}/categories`).subscribe({
      next: (items) => {
        this.archives = (items ?? []).map((i) => i.name);
      },
      error: () => {
        // ignore
      }
    });
  }

  onArchiveChange(event: Event) {
    const target = event.target as HTMLSelectElement | null;
    if (!target) {
      return;
    }
    const val = target.value;
    if (!val) {
      return;
    }
    this.form.archive = val;
  }

  generateSlug(mode: 'llm' | 'pinyin') {
    if (!this.form.title.trim()) {
      this.error = '请先填写标题';
      return;
    }
    this.slugGenerating = true;
    this.slugMessage = '';
    this.error = '';
    this.http
      .post<{ slug: string; source: string }>(
        `${API_BASE}/slug`,
        { title: this.form.title, mode },
        { withCredentials: true }
      )
      .subscribe({
        next: (res) => {
          this.form.slug = res.slug;
          this.slugMessage = res.source === 'llm' ? '已使用 DeepSeek 生成' : '已使用拼音生成';
          this.slugGenerating = false;
        },
        error: (err: any) => {
          this.slugGenerating = false;
          this.error = err?.error?.error || '生成 slug 失败';
        }
      });
  }

  editorActive(type: string, attrs?: Record<string, any>): boolean {
    return this.editor ? this.editor.isActive(type as any, attrs as any) : false;
  }

  toggleHeading(level: 1 | 2 | 3): void {
    this.editor?.chain().focus().toggleHeading({ level }).run();
  }

  toggleBold(): void {
    this.editor?.chain().focus().toggleBold().run();
  }

  toggleItalic(): void {
    this.editor?.chain().focus().toggleItalic().run();
  }

  toggleStrike(): void {
    this.editor?.chain().focus().toggleStrike().run();
  }

  toggleInlineCode(): void {
    this.editor?.chain().focus().toggleCode().run();
  }

  toggleCodeBlock(): void {
    this.editor?.chain().focus().toggleCodeBlock().run();
  }

  toggleBulletList(): void {
    this.editor?.chain().focus().toggleBulletList().run();
  }

  toggleOrderedList(): void {
    this.editor?.chain().focus().toggleOrderedList().run();
  }

  toggleBlockquote(): void {
    this.editor?.chain().focus().toggleBlockquote().run();
  }

  insertHorizontalRule(): void {
    this.editor?.chain().focus().setHorizontalRule().run();
  }

  private initialEditorHTML(): string {
    const html = (this.form.bodyHtml || '').trim();
    if (html) return html;
    const md = (this.form.bodyMd || '').trim();
    if (!md) return '';
    const parsed = marked.parse(md, { breaks: true });
    return typeof parsed === 'string' ? parsed : '';
  }

  private syncEditorFromForm(): void {
    if (!this.editor) return;
    const html = this.initialEditorHTML();
    this.editor.commands.setContent(html, false);
    this.form.bodyHtml = this.editor.getHTML();
    this.form.bodyMd = tiptapToMarkdown(this.editor.getJSON());
    this.markdownHtml = this.renderMarkdown(this.form.bodyMd);
  }

  setEditorView(view: 'rich' | 'source' | 'preview'): void {
    if (this.editorView === view) return;

    if (this.editorView === 'rich') {
      this.syncFromEditor();
    }

    if (view === 'preview') {
      this.markdownHtml = this.renderMarkdown(this.form.bodyMd);
    }

    this.editorView = view;

    if (view === 'rich') {
      this.syncEditorFromForm();
      this.editor?.commands.focus();
    }
  }

  onMarkdownChange(md: string): void {
    this.form.bodyMd = md;
    this.markdownHtml = this.renderMarkdown(md);
    this.form.bodyHtml = this.markdownHtml;
  }

  private syncFromEditor(): void {
    if (!this.editor) return;
    this.form.bodyHtml = this.editor.getHTML();
    this.form.bodyMd = tiptapToMarkdown(this.editor.getJSON());
    this.markdownHtml = this.renderMarkdown(this.form.bodyMd);
  }

  private renderMarkdown(md: string): string {
    const parsed = marked.parse(md || '', { breaks: true });
    return typeof parsed === 'string' ? parsed : '';
  }
}
