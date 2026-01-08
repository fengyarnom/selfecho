import { Injectable } from '@angular/core';
import { BehaviorSubject, Observable } from 'rxjs';

@Injectable({ providedIn: 'root' })
export class SiteTitleService {
  private readonly titleSubject = new BehaviorSubject<string>('Selfecho');

  get title$(): Observable<string> {
    return this.titleSubject.asObservable();
  }

  get title(): string {
    return this.titleSubject.value;
  }

  setTitle(title: string): void {
    const next = title?.trim();
    if (next) this.titleSubject.next(next);
  }
}

