import { inject } from '@angular/core';
import { CanActivateFn, Router, UrlTree } from '@angular/router';
import { HttpClient } from '@angular/common/http';
import { API_BASE } from './api.config';
import { catchError, map, of } from 'rxjs';

export const authGuard: CanActivateFn = () => {
  const http = inject(HttpClient);
  const router = inject(Router);

  return http.get(`${API_BASE}/auth/me`, { withCredentials: true }).pipe(
    map(() => true as boolean | UrlTree),
    catchError(() => {
      return of(router.parseUrl('/login'));
    })
  );
};
