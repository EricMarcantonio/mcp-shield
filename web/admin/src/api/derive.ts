/**
 * Turning what the API returns into what an administrator has to decide with.
 *
 * Three jobs live here:
 *
 *  1. Ranking a diff. The gateway reports "changed" as three booleans; a
 *     changed input schema and a changed sentence of prose are not the same
 *     event, and the console must not flatten them into one list.
 *  2. Describing a diff in one line, for the headline of a pending manifest.
 *     It is a description of what changed and nothing more — this product
 *     deliberately carries no risk classification (see D8 in
 *     docs/superpowers/specs/2026-07-25-oss-hardening-design.md), because a
 *     label like HIGH attaches authority to a judgement the tool cannot make.
 *  3. Reconstructing the capability set an approved manifest covers. The
 *     gateway stores every manifest's canonical JSON but serves it on no
 *     route, so the set is replayed from the chain of stored diffs — and the
 *     replay is *checked* against those diffs, so the console can say whether
 *     what it is showing is sound rather than assert it.
 */

import { pluralise } from '../lib/format';
import type { Manifest, ManifestDiff, PendingManifest, Server } from './types';

// ── Ranking a diff ──────────────────────────────────────────────────────────

/**
 * The kinds of change, most consequential first. The order is the order the
 * console renders them in, and it is a judgement about consequence: an input
 * contract that changed can accept arguments the approver never saw; a
 * description that changed cannot.
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

/** What a manifest's capabilities can be: changed in some way, or not. */
export type CapabilityStatus = ChangeKind | 'unchanged';

export const DOMAINS = ['tool', 'prompt', 'resource'] as const;
export type Domain = (typeof DOMAINS)[number];

export interface Capability {
  status: CapabilityStatus;
  domain: Domain;
  /** Tool or prompt name, or resource URI: whatever the upstream asserted. */
  identity: string;
  /** Whether the gate withholds this item until the manifest is approved. */
  withheld: boolean;
}

export const STATUS_COPY: Record<CapabilityStatus, { title: string; note: string }> = {
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
  unchanged: {
    title: 'Unchanged',
    note: 'Byte for byte identical to the approved baseline, so the gate keeps admitting it whatever is decided here.',
  },
};

/** The short word each status wears on a capability row. */
export const STATUS_LABEL: Record<CapabilityStatus, string> = {
  schema: 'Contract changed',
  added: 'New',
  arguments: 'Arguments changed',
  mime: 'Content type changed',
  description: 'Description changed',
  removed: 'No longer advertised',
  unchanged: 'Unchanged',
};

/**
 * A removed capability is the one class the gate does not withhold: the
 * upstream is no longer offering it, so there is nothing to hold back.
 * Everything else — added, changed, even description-only — is withheld until
 * the manifest is approved (internal/approval/workflow.go, unchangedSets).
 */
function isWithheld(status: CapabilityStatus): boolean {
  return status !== 'removed' && status !== 'unchanged';
}

/** Every capability this manifest changes, relative to the approved baseline. */
export function changedCapabilities(diff: ManifestDiff | null): Capability[] {
  if (!diff) return [];

  const found: Capability[] = [];
  const push = (status: ChangeKind, domain: Domain, identity: string) =>
    found.push({ status, domain, identity, withheld: isWithheld(status) });

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

  return found.sort(byConsequenceThenName);
}

/**
 * The capabilities this manifest leaves exactly as the baseline has them.
 *
 * They matter to the person deciding: they are what keeps working whichever
 * way the decision goes, and they are the difference between "this server is
 * changing one tool" and "this server is being rebuilt".
 */
export function unchangedCapabilities(baseline: CapabilitySet, changed: Capability[]): Capability[] {
  const touched = new Set(changed.map(identityKey));

  const named: [Domain, string[]][] = [
    ['tool', baseline.tools],
    ['prompt', baseline.prompts],
    ['resource', baseline.resources],
  ];

  return named
    .flatMap(([domain, identities]) =>
      identities.map((identity) => ({
        status: 'unchanged' as const,
        domain,
        identity,
        withheld: false,
      })),
    )
    .filter((capability) => !touched.has(identityKey(capability)))
    .sort(byConsequenceThenName);
}

function identityKey(capability: Capability): string {
  return `${capability.domain}:${capability.identity}`;
}

function byConsequenceThenName(a: Capability, b: Capability): number {
  const rank = statusRank(a.status) - statusRank(b.status);
  return rank !== 0 ? rank : a.identity.localeCompare(b.identity);
}

function statusRank(status: CapabilityStatus): number {
  const index = CHANGE_KINDS.indexOf(status as ChangeKind);
  return index === -1 ? CHANGE_KINDS.length : index;
}

/** How many capabilities this manifest holds back until somebody decides. */
export function withheldCount(capabilities: Capability[]): number {
  return capabilities.filter((capability) => capability.withheld).length;
}

// ── Describing a diff in one line ───────────────────────────────────────────

const CHANGE_PHRASE: Record<ChangeKind, (count: number, domain: Domain) => string> = {
  schema: (n) => `${n} input ${pluralise(n, 'contract')} changed`,
  added: (n, domain) => `${n} new ${pluralise(n, domain)}`,
  arguments: (n) => `${n} prompt argument ${pluralise(n, 'list')} changed`,
  mime: (n) => `${n} resource content ${pluralise(n, 'type')} changed`,
  description: (n) => `${n} ${pluralise(n, 'description')} changed`,
  removed: (n, domain) => `${n} ${pluralise(n, domain)} no longer advertised`,
};

/** Only these read differently per domain; the rest name one domain already. */
const SPLIT_BY_DOMAIN: ReadonlySet<ChangeKind> = new Set<ChangeKind>(['added', 'removed']);

/**
 * One line describing what a manifest changes. A description of the diff, not
 * a verdict on it: nothing here ranks a capability as dangerous, because
 * nothing in this system can.
 */
export function summariseChanges(capabilities: Capability[], hasBaseline: boolean): string {
  if (!hasBaseline) return 'First connection — no approved baseline';

  const phrases = CHANGE_KINDS.flatMap((kind) => phrasesFor(kind, capabilities));
  if (phrases.length === 0) return 'No capability change';
  return phrases.join(', ');
}

function phrasesFor(kind: ChangeKind, capabilities: Capability[]): string[] {
  const matching = capabilities.filter((capability) => capability.status === kind);
  const first = matching[0];
  if (!first) return [];
  if (!SPLIT_BY_DOMAIN.has(kind)) return [CHANGE_PHRASE[kind](matching.length, first.domain)];

  return DOMAINS.flatMap((domain) => {
    const count = matching.filter((capability) => capability.domain === domain).length;
    return count === 0 ? [] : [CHANGE_PHRASE[kind](count, domain)];
  });
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

export const EMPTY_CAPABILITY_SET: CapabilitySet = { tools: [], prompts: [], resources: [] };

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

/** How many capabilities a reconstructed set holds, across all three kinds. */
export function capabilityCount(set: CapabilitySet): number {
  return set.tools.length + set.prompts.length + set.resources.length;
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
