/**
 * Turning what the API returns into what an administrator has to decide with.
 *
 * Two jobs live here:
 *
 *  1. Ranking a diff. The gateway reports "changed" as three booleans; a
 *     changed input schema and a changed sentence of prose are not the same
 *     event, and the console must not flatten them into one list.
 *  2. Reconstructing the capability set an approved manifest covers. The
 *     gateway stores every manifest's canonical JSON but serves it on no
 *     route, so the set is replayed from the chain of stored diffs — and the
 *     replay is *checked* against those diffs, so the console can say whether
 *     what it is showing is sound rather than assert it.
 */

import type { Manifest, ManifestDiff, PendingManifest, Server } from './types';

// ── Ranking a diff ──────────────────────────────────────────────────────────

/**
 * The kinds of change, most consequential first. The order is the order the
 * console renders them in, and it is a judgement about risk: an input contract
 * that changed can accept arguments the approver never saw; a description that
 * changed cannot.
 */
export const CHANGE_KINDS = [
  'schema',
  'added',
  'arguments',
  'mime',
  'description',
  'removed',
] as const;
export type ChangeKind = (typeof CHANGE_KINDS)[number];

export type Domain = 'tool' | 'prompt' | 'resource';

export interface ChangeItem {
  kind: ChangeKind;
  domain: Domain;
  /** Tool or prompt name, or resource URI: whatever the upstream asserted. */
  identity: string;
  /** Whether the gate withholds this item until the manifest is approved. */
  withheld: boolean;
}

export interface ChangeGroup {
  kind: ChangeKind;
  /** Editorial heading — ours, not the gateway's. */
  title: string;
  /** What the change means for the person deciding. */
  note: string;
  items: ChangeItem[];
}

const GROUP_COPY: Record<ChangeKind, { title: string; note: string }> = {
  schema: {
    title: 'Input contract changed',
    note: 'This tool now accepts different arguments. Read the new schema before admitting it — an argument nobody approved is an argument the model can be steered into filling.',
  },
  added: {
    title: 'New capability',
    note: 'The upstream server did not advertise this before. Nothing new reaches a client until the manifest carrying it is approved.',
  },
  arguments: {
    title: 'Prompt arguments changed',
    note: 'Prompt arguments feed straight into the model. Treat a changed argument list the way you treat a changed input schema.',
  },
  mime: {
    title: 'Resource content type changed',
    note: 'The upstream is serving this URI as a different kind of content than the one you approved.',
  },
  description: {
    title: 'Description changed',
    note: 'Text only. The input contract is unchanged. Withheld all the same, because the description is what the model reads to decide when to call a tool.',
  },
  removed: {
    title: 'No longer advertised',
    note: 'The upstream server stopped offering this. Nothing to admit — it simply stops appearing.',
  },
};

/**
 * A removed capability is the one class the gate does not withhold: the
 * upstream is no longer offering it, so there is nothing to hold back.
 * Everything else — added, changed, even description-only — is withheld until
 * the manifest is approved (internal/approval/workflow.go, unchangedSets).
 */
function isWithheld(kind: ChangeKind): boolean {
  return kind !== 'removed';
}

export function toChangeItems(diff: ManifestDiff | null): ChangeItem[] {
  if (!diff) return [];

  const items: ChangeItem[] = [];
  const push = (kind: ChangeKind, domain: Domain, identity: string) =>
    items.push({ kind, domain, identity, withheld: isWithheld(kind) });

  for (const name of diff.added_tools ?? []) push('added', 'tool', name);
  for (const name of diff.added_prompts ?? []) push('added', 'prompt', name);
  for (const uri of diff.added_resources ?? []) push('added', 'resource', uri);

  for (const name of diff.removed_tools ?? []) push('removed', 'tool', name);
  for (const name of diff.removed_prompts ?? []) push('removed', 'prompt', name);
  for (const uri of diff.removed_resources ?? []) push('removed', 'resource', uri);

  // A tool that changed both schema and description is ranked by the schema:
  // that is the part that changes what it can be made to do.
  for (const tc of diff.changed_tools ?? []) {
    push(tc.schema_changed ? 'schema' : 'description', 'tool', tc.name);
  }
  for (const pc of diff.changed_prompts ?? []) {
    push(pc.arguments_changed ? 'arguments' : 'description', 'prompt', pc.name);
  }
  for (const rc of diff.changed_resources ?? []) {
    push(rc.mime_type_changed ? 'mime' : 'description', 'resource', rc.uri);
  }

  return items;
}

export function groupChanges(items: ChangeItem[]): ChangeGroup[] {
  return CHANGE_KINDS.map((kind) => ({
    kind,
    ...GROUP_COPY[kind],
    items: items
      .filter((item) => item.kind === kind)
      .sort((a, b) => a.identity.localeCompare(b.identity)),
  })).filter((group) => group.items.length > 0);
}

/** How many capabilities this manifest holds back until somebody decides. */
export function withheldCount(items: ChangeItem[]): number {
  return items.filter((item) => item.withheld).length;
}

/** The heaviest change in a manifest, for at-a-glance ranking of the queue. */
export function topKind(items: ChangeItem[]): ChangeKind | null {
  for (const kind of CHANGE_KINDS) {
    if (items.some((item) => item.kind === kind)) return kind;
  }
  return null;
}

// ── The approved baseline, per server ───────────────────────────────────────

export interface ServerLedger {
  server: Server;
  /** The manifest currently admitted, or null when nothing is approved yet. */
  approved: Manifest | null;
  pending: Manifest[];
  /** Everything the gateway has recorded for this server, newest first. */
  history: Manifest[];
}

export function buildServerLedgers(servers: Server[], manifests: Manifest[]): ServerLedger[] {
  return servers.map((server) => {
    const mine = manifests.filter((m) => m.server === server.name);
    return {
      server,
      approved: mine.find((m) => m.state === 'APPROVED') ?? null,
      pending: mine.filter((m) => m.state === 'PENDING').sort((a, b) => b.id - a.id),
      history: [...mine].sort((a, b) => b.id - a.id),
    };
  });
}

// ── Replaying the capability set ────────────────────────────────────────────

export interface CapabilitySet {
  tools: string[];
  prompts: string[];
  resources: string[];
}

export type Reconstruction =
  | { sound: true; set: CapabilitySet; chainLength: number }
  | { sound: false; reason: string };

/**
 * Replay a server's approval chain into the capability set its approved
 * manifest admits.
 *
 * Every manifest row carries the diff computed against the approved baseline
 * in force when the row was written, so replaying the chain of manifests that
 * have *held* the baseline — the superseded ones, then the approved one —
 * reproduces the set. That holds only if each diff really was computed against
 * its predecessor, which is not guaranteed by anything the API exposes, so
 * each step is checked: a removal or change must name something already in the
 * set, an addition must not. A chain that fails the check is reported unsound
 * rather than rendered, because a capability list an operator cannot trust is
 * worse than none.
 *
 * Names only. Descriptions and input schemas live in the manifest's canonical
 * JSON, which no route serves.
 */
export function replayCapabilities(
  chain: { manifest: Manifest; diff: ManifestDiff | null }[],
): Reconstruction {
  const tools = new Set<string>();
  const prompts = new Set<string>();
  const resources = new Set<string>();

  for (const { manifest, diff } of chain) {
    if (!diff) {
      return {
        sound: false,
        reason: `manifest ${manifest.hash.slice(0, 8)} has no stored diff`,
      };
    }

    const steps: [Set<string>, string[], string[], string[]][] = [
      [tools, diff.added_tools, diff.removed_tools, (diff.changed_tools ?? []).map((c) => c.name)],
      [
        prompts,
        diff.added_prompts,
        diff.removed_prompts,
        (diff.changed_prompts ?? []).map((c) => c.name),
      ],
      [
        resources,
        diff.added_resources,
        diff.removed_resources,
        (diff.changed_resources ?? []).map((c) => c.uri),
      ],
    ];

    for (const [set, added, removed, changed] of steps) {
      for (const name of added ?? []) {
        if (set.has(name)) {
          return { sound: false, reason: `${name} was added twice in the approval chain` };
        }
        set.add(name);
      }
      for (const name of removed ?? []) {
        if (!set.has(name)) {
          return { sound: false, reason: `${name} was removed without ever being admitted` };
        }
        set.delete(name);
      }
      // A changed capability keeps its identity; it must already be present.
      for (const name of changed) {
        if (!set.has(name)) {
          return { sound: false, reason: `${name} changed without ever being admitted` };
        }
      }
    }
  }

  return {
    sound: true,
    chainLength: chain.length,
    set: {
      tools: [...tools].sort(),
      prompts: [...prompts].sort(),
      resources: [...resources].sort(),
    },
  };
}

/**
 * The manifests that have held a server's baseline, oldest first: the ones
 * that were superseded, then the one approved now. Insert order (id) is the
 * only ordering the API exposes; approvals are sequential, so it matches.
 */
export function baselineChain(ledger: ServerLedger): Manifest[] {
  return ledger.history
    .filter((m) => m.state === 'SUPERSEDED' || m.state === 'APPROVED')
    .sort((a, b) => a.id - b.id);
}

// ── Queue ordering ──────────────────────────────────────────────────────────

/** Oldest first: the thing that has been withheld longest is the first ask. */
export function byAge(a: PendingManifest, b: PendingManifest): number {
  return Date.parse(a.created_at) - Date.parse(b.created_at);
}
