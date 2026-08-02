/**
 * The small vocabulary the whole console is written in.
 *
 * Everything here is a thin wrapper over the design system in ds/organic.css:
 * `.card`, `.tag`, `.btn`, `.table`. The wrappers exist so a rule like "a
 * status tag always carries its word, never just its hue" is enforced in one
 * place instead of remembered in twelve.
 *
 * The typographic rule the design keeps: monospace is machine-truth. A tool
 * name, a manifest hash, an event type — the upstream server or the gateway
 * asserted those. Headings, notes and button labels are ours, and are set in
 * the body face.
 */

import type { ReactNode } from 'react';
import { shortHash } from '../lib/format';
import type { Capability, CapabilityStatus } from '../api/derive';
import { STATUS_LABEL } from '../api/derive';
import type { ManifestState } from '../api/types';

// ── Machine truth ───────────────────────────────────────────────────────────

/** A tool name, prompt name, resource URI, event type or hash. */
export function Mono({ children, title }: { children: ReactNode; title?: string }) {
  return (
    <span className="mono text-[13px]" title={title}>
      {children}
    </span>
  );
}

/** A manifest's identity. The full hash is available on hover and on copy. */
export function Hash({ value }: { value: string }) {
  return <Mono title={value}>{shortHash(value)}</Mono>;
}

// ── Tags ────────────────────────────────────────────────────────────────────

export type TagTone = 'accent' | 'accent-2' | 'neutral' | 'outline';

export function Tag({ tone, children }: { tone: TagTone; children: ReactNode }) {
  return <span className={`tag tag-${tone}`}>{children}</span>;
}

/**
 * Colour is never the only carrier: every tag below spells out its state, so
 * the hue is a second reading of something the word already said.
 */
const STATUS_TONE: Record<CapabilityStatus, TagTone> = {
  schema: 'outline',
  added: 'accent',
  arguments: 'outline',
  mime: 'outline',
  description: 'accent-2',
  removed: 'neutral',
  unchanged: 'neutral',
};

export function StatusTag({ status }: { status: CapabilityStatus }) {
  return <Tag tone={STATUS_TONE[status]}>{STATUS_LABEL[status]}</Tag>;
}

/**
 * A capability chip: the name the upstream asserted, toned by what happened
 * to it. The tone is the second reading — the enclosing card already states
 * the change in words, and the word rides along here for assistive tech.
 */
export function CapabilityTag({ capability }: { capability: Capability }) {
  return (
    <Tag tone={STATUS_TONE[capability.status]}>
      {capability.identity}
      <span className="sr-only"> — {STATUS_LABEL[capability.status]}</span>
    </Tag>
  );
}

const MANIFEST_STATE_TONE: Record<ManifestState, TagTone> = {
  PENDING: 'accent',
  APPROVED: 'accent-2',
  REJECTED: 'outline',
  SUPERSEDED: 'neutral',
};

const MANIFEST_STATE_WORD: Record<ManifestState, string> = {
  PENDING: 'Pending',
  APPROVED: 'Approved',
  REJECTED: 'Rejected',
  SUPERSEDED: 'Superseded',
};

export function StateTag({ state }: { state: ManifestState }) {
  return <Tag tone={MANIFEST_STATE_TONE[state]}>{MANIFEST_STATE_WORD[state]}</Tag>;
}

// ── Page furniture ──────────────────────────────────────────────────────────

export function PageHeading({ title, note }: { title: string; note?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline gap-x-[var(--space-3)] gap-y-[var(--space-1)]">
      <h1 className="m-0 text-[28px]">{title}</h1>
      {note && <p className="text-muted m-0 text-sm">{note}</p>}
    </div>
  );
}

/** A heading for a block inside a page. `.card-kicker` is the design's rule. */
export function SectionKicker({ children }: { children: ReactNode }) {
  return <h2 className="card-kicker m-0 mb-[var(--space-2)]">{children}</h2>;
}

/**
 * An empty state that says what the emptiness means. A server with nothing
 * approved is a normal, important, fail-closed state — not a fault — so the
 * copy, not the colour, carries which of the two this is.
 */
export function EmptyState({
  kicker,
  title,
  children,
  action,
}: {
  kicker?: ReactNode;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="card items-center px-[var(--space-6)] py-[var(--space-8)] text-center">
      {kicker && <p className="card-kicker m-0">{kicker}</p>}
      <h2 className="m-0 mb-[var(--space-2)] text-[20px]">{title}</h2>
      {children && (
        <div className="text-muted m-0 max-w-[60ch] text-sm [&>p]:mb-[var(--space-2)] [&>p:last-child]:mb-0">
          {children}
        </div>
      )}
      {action && <div className="mt-[var(--space-3)]">{action}</div>}
    </div>
  );
}

/**
 * A field the gateway has no route for. Said plainly and quietly, because a
 * console for a security gateway that fills a gap with something plausible is
 * worse than one that admits the gap.
 */
export function Unavailable({ children }: { children: ReactNode }) {
  return <p className="text-muted m-0 text-[13px] italic">{children}</p>;
}

export function Loading({ label }: { label: string }) {
  return (
    <p role="status" className="text-muted m-0 py-[var(--space-8)] text-center text-sm">
      {label}
    </p>
  );
}

/** A table that scrolls inside itself rather than pushing the page sideways. */
export function TableFrame({ children }: { children: ReactNode }) {
  return (
    <div className="-mx-[var(--space-2)] overflow-x-auto px-[var(--space-2)]">
      <div className="min-w-[640px]">{children}</div>
    </div>
  );
}
