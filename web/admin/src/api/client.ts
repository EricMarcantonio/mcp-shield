/**
 * The only module in this application that talks HTTP.
 *
 * ── The auth seam ────────────────────────────────────────────────────────────
 * Every request goes through `request()`, and `request()` asks
 * `authHeaderProvider` for headers. There is no authentication today by
 * design; when Keycloak lands, `setAuthHeaderProvider` is called once with a
 * function returning `{ Authorization: 'Bearer …' }` and nothing else in the
 * codebase changes. A 401 handler would go in `request()` too, beside the
 * ApiError throw. No component imports fetch.
 */

import { apiBaseUrl } from '../lib/config';
import type {
  ApprovalRecord,
  FailedNotification,
  Manifest,
  ManifestContents,
  ManifestDiff,
  PendingManifest,
  Server,
  WireServer,
} from './types';

export class ApiError extends Error {
  readonly status: number;
  readonly url: string;

  constructor(status: number, url: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.url = url;
  }

  get isNotFound(): boolean {
    return this.status === 404;
  }

  /** The gateway returns 409 when a manifest is no longer PENDING. */
  get isConflict(): boolean {
    return this.status === 409;
  }
}

/** Raised when the gateway cannot be reached at all, as opposed to refusing. */
export class UnreachableError extends Error {
  readonly url: string;

  constructor(url: string, cause: unknown) {
    super(`The gateway at ${url || 'this origin'} did not answer.`);
    this.name = 'UnreachableError';
    this.url = url;
    this.cause = cause;
  }
}

/**
 * A 2xx that is not JSON. Both the gateway's own router and an SPA host with a
 * catch-all rewrite answer an unknown /api path with HTML rather than a 404,
 * so "200 text/html" is how a route that does not exist actually presents.
 */
export class RouteNotServedError extends Error {
  readonly path: string;

  constructor(path: string) {
    super(`${path} is not served by this gateway.`);
    this.name = 'RouteNotServedError';
    this.path = path;
  }
}

type HeaderProvider = () => Record<string, string>;

let authHeaderProvider: HeaderProvider = () => ({});

/** The single edit point for attaching credentials to every API call. */
export function setAuthHeaderProvider(provider: HeaderProvider): void {
  authHeaderProvider = provider;
}

function url(path: string): string {
  return `${apiBaseUrl}${path}`;
}

interface RequestOptions extends RequestInit {
  /** Reject a 2xx whose body is not JSON. Used when probing optional routes. */
  requireJson?: boolean;
}

async function request<T>(path: string, options?: RequestOptions): Promise<T> {
  const { requireJson, ...init } = options ?? {};
  const target = url(path);

  let response: Response;
  try {
    response = await fetch(target, {
      ...init,
      headers: {
        Accept: 'application/json',
        ...(init.body ? { 'Content-Type': 'application/json' } : {}),
        ...authHeaderProvider(),
        ...init.headers,
      },
    });
  } catch (cause) {
    throw new UnreachableError(apiBaseUrl, cause);
  }

  const body = await response.text();

  if (!response.ok) {
    throw new ApiError(response.status, target, errorMessage(body, response.status));
  }

  if (requireJson && !(response.headers.get('content-type') ?? '').includes('json')) {
    throw new RouteNotServedError(path);
  }

  // GET /api/manifests/{id}/diff answers the bare literal `null` when a
  // manifest had no baseline to diff against.
  const trimmed = body.trim();
  if (trimmed === '' || trimmed === 'null') {
    return null as T;
  }

  try {
    return JSON.parse(trimmed) as T;
  } catch {
    throw new RouteNotServedError(path);
  }
}

/** The gateway's error shape is `{"error": "..."}`; fall back to the status. */
function errorMessage(body: string, status: number): string {
  try {
    const parsed: unknown = JSON.parse(body);
    if (parsed && typeof parsed === 'object' && 'error' in parsed) {
      const message: unknown = parsed.error;
      if (typeof message === 'string' && message.trim() !== '') return message;
    }
  } catch {
    // Not JSON. Fall through to the body text, then the status line.
  }
  return body.trim() || `HTTP ${status}`;
}

// ── Routes the gateway serves today ─────────────────────────────────────────

/** GET /api/servers. Go field names in, ordinary field names out. */
export async function listServers(): Promise<Server[]> {
  const wire = await request<WireServer[]>('/api/servers');
  return (wire ?? []).map((s) => ({
    id: s.ID,
    name: s.Name,
    endpoint: s.Endpoint,
    createdAt: s.CreatedAt,
  }));
}

export async function listPendingManifests(): Promise<PendingManifest[]> {
  return (await request<PendingManifest[]>('/api/manifests/pending')) ?? [];
}

export async function getManifest(id: number): Promise<Manifest> {
  return request<Manifest>(`/api/manifests/${id}`, { requireJson: true });
}

export async function getManifestDiff(id: number): Promise<ManifestDiff | null> {
  return request<ManifestDiff | null>(`/api/manifests/${id}/diff`);
}

export async function approveManifest(id: number, username: string, reason: string): Promise<void> {
  await request(`/api/manifests/${id}/approve`, {
    method: 'POST',
    body: JSON.stringify({ username, reason }),
  });
}

export async function rejectManifest(id: number, username: string, reason: string): Promise<void> {
  await request(`/api/manifests/${id}/reject`, {
    method: 'POST',
    body: JSON.stringify({ username, reason }),
  });
}

/**
 * GET /api/notifications/failed.
 *
 * The route 404s deliberately when no webhook targets are configured — the
 * gateway distinguishes "nothing to deliver to" from "everything delivered",
 * and so does this console. `null` means the surface does not exist here.
 */
export async function listFailedNotifications(): Promise<FailedNotification[] | null> {
  return optional<FailedNotification[]>('/api/notifications/failed');
}

export interface Health {
  status: string;
}

/** /healthz answers 200 `{"status":"ok"}` without a JSON content type. */
export async function getHealth(): Promise<Health> {
  return request<Health>('/healthz');
}

// ── Routes the gateway does not serve yet ───────────────────────────────────
//
// Each of these is probed and degrades to `null`, which the views render as a
// named gap rather than as an empty result. They light up on their own when
// the API grows the route; nothing else has to change.

/** Decision history for one manifest. `null` = no such route on this gateway. */
export async function getManifestApprovals(id: number): Promise<ApprovalRecord[] | null> {
  return optional<ApprovalRecord[]>(`/api/manifests/${id}/approvals`);
}

/** The capability set a manifest covers. `null` = no such route. */
export async function getManifestContents(id: number): Promise<ManifestContents | null> {
  return optional<ManifestContents>(`/api/manifests/${id}/contents`);
}

export interface ManifestIndex {
  manifests: Manifest[];
  /** True when the list was reconstructed by probing rather than served. */
  probed: boolean;
}

/**
 * Every manifest the gateway knows about.
 *
 * There is no list route yet, and no route reporting which manifest is a
 * server's approved baseline — so without this the console cannot answer "what
 * is approved right now" at all.
 *
 * The fallback finds the highest manifest id by doubling and then bisecting,
 * and reads 1..highest. Manifest ids are contiguous from 1 — they are SQLite
 * rowids on an insert-only table with no delete path anywhere in the store —
 * so the boundary is exact, and finding it this way costs a couple of misses
 * rather than a scan. All of it goes away the moment `GET /api/manifests`
 * exists.
 */
export async function listAllManifests(): Promise<ManifestIndex> {
  const served = await optional<Manifest[]>('/api/manifests');
  if (served) return { manifests: served, probed: false };
  return { manifests: await walkManifestIds(), probed: true };
}

/** Refuse to probe past this, however the gateway is behaving. */
const WALK_CAP = 4096;

async function walkManifestIds(): Promise<Manifest[]> {
  const highest = await highestManifestId();
  if (highest === 0) return [];

  const ids = Array.from({ length: highest }, (_, i) => i + 1);
  const rows = await Promise.all(ids.map(optionalManifest));
  return rows.filter((m): m is Manifest => m !== null).sort((a, b) => a.id - b.id);
}

async function highestManifestId(): Promise<number> {
  // Double until a miss: `present` is the last id known to exist, `absent` the
  // first known not to.
  let present = 0;
  let absent = 1;
  while (absent <= WALK_CAP && (await optionalManifest(absent))) {
    present = absent;
    absent *= 2;
  }
  if (present === 0) return 0;

  // Bisect the boundary between them.
  let low = present;
  let high = Math.min(absent, WALK_CAP + 1);
  while (high - low > 1) {
    const middle = Math.floor((low + high) / 2);
    if (await optionalManifest(middle)) low = middle;
    else high = middle;
  }
  return low;
}

async function optionalManifest(id: number): Promise<Manifest | null> {
  return absentAsNull(getManifest(id));
}

async function optional<T>(path: string): Promise<T | null> {
  return absentAsNull(request<T>(path, { requireJson: true }));
}

async function absentAsNull<T>(pending: Promise<T>): Promise<T | null> {
  try {
    return await pending;
  } catch (error) {
    if (error instanceof RouteNotServedError) return null;
    if (error instanceof ApiError && error.isNotFound) return null;
    throw error;
  }
}
