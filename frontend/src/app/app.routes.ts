import { Routes } from '@angular/router';
import { HomeComponent } from './home/home.component';
import { AdminComponent } from './admin/admin.component';
import { AdminFormComponent } from './admin-form/admin-form.component';
import { ArchiveComponent } from './archive/archive.component';
import { CategoriesComponent } from './categories/categories.component';
import { AdminArchiveComponent } from './admin-archive/admin-archive.component';
import { AdminHealthComponent } from './admin-health/admin-health.component';
import { LoginComponent } from './login/login.component';
import { authGuard } from './auth.guard';

export const routes: Routes = [
  { path: '', component: HomeComponent },
  { path: 'archive', component: ArchiveComponent },
  { path: 'categories', component: CategoriesComponent },
  { path: 'login', component: LoginComponent },
  { path: 'admin', redirectTo: 'admin/posts', pathMatch: 'full' },
  { path: 'admin/posts', component: AdminComponent, canActivate: [authGuard] },
  { path: 'admin/archive', component: AdminArchiveComponent, canActivate: [authGuard] },
  { path: 'admin/health', component: AdminHealthComponent, canActivate: [authGuard] },
  { path: 'admin/new', component: AdminFormComponent, canActivate: [authGuard] },
  { path: 'admin/edit/:id', component: AdminFormComponent, canActivate: [authGuard] }
];
