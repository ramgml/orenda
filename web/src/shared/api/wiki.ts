import type { ApiClient } from './core';
import type { TaskAttachment } from './taskDetails';

export interface WikiPage {
  id: string;
  parent_id?: string;
  slug: string;
  title: string;
  content_md?: string;
  position: number;
  content_format?: string;
  /**
   * Human-readable sequential wiki page number ("W5"). Assigned by the
   * storage layer on CreatePage from the wiki_number_seq high-watermark.
   */
  number: number;
  created_at: string;
  updated_at: string;
}

/**
 * One block in a wiki page's render tree. Mirrors BlockNote's block
 * shape for direct round-trip without transformation.
 */
export interface WikiBlock {
  id: string;
  type: string;
  props?: Record<string, unknown>;
  content?: unknown;
  children?: WikiBlock[];
}

export interface Notification {
  id: string;
  user_id: string;
  type: string;
  target_type?: string;
  target_id?: string;
  payload: string; // JSON: { title, body, link, meta }
  read_at?: string;
  dedup_key: string;
  created_at: string;
}

/**
 * Response from GET /api/v1/pages/{slug}/blocks.
 * - format "markdown": content_md contains raw markdown (legacy page)
 * - format "blocks": blocks array contains the BlockNote-shaped tree
 */
interface WikiBlockView {
  format: string;
  content_md?: string;
  blocks?: WikiBlock[];
}

export interface WikiTreeNode {
  page: WikiPage;
  children?: WikiTreeNode[];
}

export interface SearchHit {
  type: 'page' | 'task' | 'comment';
  id: string;
  slug?: string; // wiki page slug, present for type=page hits
  title?: string;
  snippet: string;
  score: number;
}

/**
 * Wiki domain endpoints (`/api/v1/pages`, blocks, search) plus
 * notifications (`/api/v1/notifications`).
 * Merged onto the ApiClient prototype in client.ts.
 */
export const wikiEndpoints = {
  // ---- Wiki (Phase 5) ----

  listPages(): Promise<{ tree: WikiTreeNode[] }> {
    return this.http.get<{ tree: WikiTreeNode[] }>('/api/v1/pages').then((r) => r.data);
  },

  getPageBySlug(slug: string): Promise<WikiPage> {
    return this.http.get<WikiPage>(`/api/v1/pages/${slug}`).then((r) => r.data);
  },

  savePage(input: {
    slug: string;
    title: string;
    content_md?: string;
    parent_id?: string;
  }): Promise<WikiPage> {
    // POST creates; PUT on /pages/{slug} updates the same record.
    return this.http.post<WikiPage>('/api/v1/pages', input).then((r) => r.data);
  },

  updatePage(
    slug: string,
    input: Partial<{
      slug: string;
      title: string;
      content_md: string;
      parent_id: string;
      position: number;
    }>,
  ): Promise<WikiPage> {
    return this.http.put<WikiPage>(`/api/v1/pages/${slug}`, input).then((r) => r.data);
  },

  getPageBacklinks(slug: string): Promise<{ backlinks: WikiPage[] }> {
    return this.http
      .get<{ backlinks: WikiPage[] }>(`/api/v1/pages/${slug}/backlinks`)
      .then((r) => r.data);
  },

  deletePage(slug: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/pages/${slug}`).then(() => undefined);
  },

  /** Move a page under a new parent. Empty parent_id → root. */
  movePage(slug: string, parent_id: string): Promise<void> {
    return this.http.patch<void>(`/api/v1/pages/${slug}/move`, { parent_id }).then(() => undefined);
  },

  // ---- Wiki Blocks (Phase 7/BlockNote) ----

  getPageBlocks(slug: string): Promise<WikiBlockView> {
    return this.http.get<WikiBlockView>(`/api/v1/pages/${slug}/blocks`).then((r) => r.data);
  },

  updatePageBlocks(slug: string, blocks: WikiBlock[]): Promise<WikiPage> {
    return this.http.put<WikiPage>(`/api/v1/pages/${slug}/blocks`, { blocks }).then((r) => r.data);
  },

  uploadPageAttachment(slug: string, file: File): Promise<TaskAttachment> {
    const form = new FormData();
    form.append('file', file);
    return this.http
      .post<TaskAttachment>(`/api/v1/pages/${slug}/attachments`, form, {
        headers: { 'Content-Type': 'multipart/form-data' },
      })
      .then((r) => r.data);
  },

  // ---- Search (Phase 5) ----

  search(params: {
    q: string;
    type?: string;
    limit?: number;
  }): Promise<{ hits: SearchHit[]; total: number }> {
    return this.http
      .get<{ hits: SearchHit[]; total: number }>('/api/v1/search', { params })
      .then((r) => r.data);
  },

  // ---- Notifications (Phase 6) ----

  listNotifications(params?: {
    limit?: number;
  }): Promise<{ notifications: Notification[]; unread: number }> {
    return this.http
      .get<{ notifications: Notification[]; unread: number }>('/api/v1/notifications', { params })
      .then((r) => r.data);
  },

  markNotificationRead(id: string): Promise<void> {
    return this.http.post<void>(`/api/v1/notifications/${id}/read`).then(() => undefined);
  },

  markAllNotificationsRead(): Promise<void> {
    return this.http.post<void>('/api/v1/notifications/read-all').then(() => undefined);
  },
} satisfies ThisType<ApiClient>;

/** Method surface contributed by this domain to the ApiClient type. */
export type WikiApi = typeof wikiEndpoints;
