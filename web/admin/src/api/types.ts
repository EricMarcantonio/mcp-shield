/**
 * The shapes the approval API actually returns today, transcribed from
 * internal/api/views.go, internal/database/models.go and internal/diff/diff.go
 * and verified against a running gateway.
 *
 * Where the gateway is inconsistent (GET /api/servers returns Go field names,
 * every other route returns snake_case) the wire shape is typed honestly here
 * and normalised once, in client.ts, rather than leaking into components.
 */

/** Manifest lifecycle states, from internal/database/models.go. */
export const MANIFEST_STATES = ['PENDING', 'APPROVED', 'REJECTED', 'SUPERSEDED'] as const;
export type ManifestState = (typeof MANIFEST_STATES)[number];

export type Decision = 'APPROVED' | 'REJECTED';

/** GET /api/servers — note the Go-cased keys; see normaliseServer(). */
export interface WireServer {
  ID: number;
  Name: string;
  Endpoint: string;
  CreatedAt: string;
}

export interface Server {
  id: number;
  name: string;
  endpoint: string;
  createdAt: string;
}

/** GET /api/manifests/pending */
export interface PendingManifest {
  id: number;
  server: string;
  hash: string;
  /** diff.Summarize lines, e.g. "Schema changed: calendar_create". */
  changes: string[] | null;
  created_at: string;
}

/** GET /api/manifests/{id} */
export interface Manifest {
  id: number;
  server: string;
  hash: string;
  state: ManifestState;
  created_at: string;
}

export interface ToolChange {
  name: string;
  description_changed: boolean;
  schema_changed: boolean;
}

export interface PromptChange {
  name: string;
  description_changed: boolean;
  arguments_changed: boolean;
}

export interface ResourceChange {
  uri: string;
  description_changed: boolean;
  mime_type_changed: boolean;
}

/** GET /api/manifests/{id}/diff — null when the manifest had no baseline. */
export interface ManifestDiff {
  added_tools: string[];
  removed_tools: string[];
  changed_tools: ToolChange[];
  added_prompts: string[];
  removed_prompts: string[];
  changed_prompts: PromptChange[];
  added_resources: string[];
  removed_resources: string[];
  changed_resources: ResourceChange[];
}

/** GET /api/notifications/failed — 404s when no targets are configured. */
export interface FailedNotification {
  event_id: number;
  event: string;
  server: string;
  manifest_id: number;
  attempts: number;
  last_error: string;
  created_at: string;
}

/**
 * One recorded decision. The gateway stores these (internal/database
 * `approvals`) and the server-rendered dashboard reads them, but no JSON route
 * exposes them yet — see the `manifestApprovals` probe in client.ts. The type
 * is defined against the stored shape so the ledger lights up the moment a
 * route lands.
 */
export interface ApprovalRecord {
  id: number;
  manifest_id: number;
  manifest_hash: string;
  server: string;
  decision: Decision;
  /** A caller-supplied attestation. The gateway does not verify it. */
  username: string;
  reason: string;
  /** When the decision was made — distinct from when its manifest was recorded. */
  decided_at: string;
}

/** Every list route wraps its rows in this envelope. */
export interface Paginated<T> {
  items: T[];
  pagination: {
    limit: number;
    offset: number;
    count: number;
    has_more: boolean;
  };
}

export interface DecisionRequest {
  username: string;
  reason: string;
}

/**
 * The manifest's capability content. The gateway stores `canonical_json` on
 * every manifest row but does not serve it on any route yet; this is the shape
 * the console will render as soon as one exists.
 */
export interface ManifestContents {
  tools: { name: string; description?: string; inputSchema?: unknown }[];
  prompts: { name: string; description?: string }[];
  resources: { uri: string; name?: string; description?: string; mimeType?: string }[];
}
