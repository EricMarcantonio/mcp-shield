/**
 * The small vocabulary the whole console is written in.
 *
 * The typographic rule these enforce: mono is machine-truth, sans is
 * editorial. A tool name, a hash, a schema fragment — the upstream server or
 * the gateway asserted those, so they are set in mono. A heading, a note, a
 * button label — we wrote those, so they are set in sans. Applied
 * consistently it tells a reader at a glance what is claimed and what is
 * commentary.
 */

import type { ReactNode } from 'react';
import { shortHash } from '../lib/format';
import type { ManifestState } from '../api/types';
import type { ChangeKind } from '../api/derive';

// ── Machine truth ───────────────────────────────────────────────────────────

/** A manifest's identity. The full hash is always available on hover/copy. */
export function Hash({ value, className = '' }: { value: string; className?: string }) {
  return (
    <span
      title={value}
      className={`font-mono text-[0.8125rem] tracking-tight text-slate ${className}`}
    >
      {shortHash(value)}
    </span>
  );
}

/** A tool name, prompt name, or resource URI: asserted by the upstream. */
export function Identity({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <span className={`font-mono text-[0.8125rem] text-ink ${className}`}>{children}</span>;
}

// ── State ───────────────────────────────────────────────────────────────────

const STATE_STYLE: Record<ManifestState, { chip: string; word: string }> = {
  PENDING: { chip: 'border-signal/40 bg-signal/10 text-signal', word: 'Pending' },
  APPROVED: { chip: 'border-slate/25 bg-slate/5 text-slate', word: 'Approved' },
  REJECTED: { chip: 'border-rule bg-paper text-slate/60', word: 'Rejected' },
  SUPERSEDED: { chip: 'border-rule bg-paper text-slate/50', word: 'Superseded' },
};

export function StateChip({ state }: { state: ManifestState }) {
  const style = STATE_STYLE[state];
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-sm border px-2 py-0.5 font-display text-micro uppercase tracking-[0.12em] ${style.chip}`}
    >
      {state === 'PENDING' && (
        <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-signal" />
      )}
      {style.word}
    </span>
  );
}

/**
 * Withheld is amber, never red — the gate holding a capability back is the
 * product working. It is also never colour alone: the word "Withheld" and a
 * bar glyph carry the same information for anyone the amber does not reach.
 */
export function HeldMark({ label = 'Withheld' }: { label?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap font-display text-micro uppercase tracking-[0.12em] text-held">
      <span aria-hidden className="inline-block h-3 w-[3px] rounded-sm bg-held" />
      {label}
    </span>
  );
}

export function AdmittedMark({ label = 'Admitted' }: { label?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap font-display text-micro uppercase tracking-[0.12em] text-slate/70">
      <span aria-hidden className="inline-block h-3 w-[3px] rounded-sm bg-slate/40" />
      {label}
    </span>
  );
}

// ── Change weight ───────────────────────────────────────────────────────────

/**
 * Weight is the design's argument about risk. A changed input schema gets a
 * solid amber rule and a heavier count; a changed description gets a hairline
 * and quiet type. They are not the same event and are not drawn as though
 * they were.
 */
export const KIND_WEIGHT: Record<ChangeKind, { rule: string; count: string; badge: string }> = {
  schema: { rule: 'bg-held', count: 'text-held', badge: 'bg-held/10 text-held border-held/30' },
  added: { rule: 'bg-held/70', count: 'text-held', badge: 'bg-held/10 text-held border-held/30' },
  arguments: {
    rule: 'bg-held/70',
    count: 'text-held',
    badge: 'bg-held/10 text-held border-held/30',
  },
  mime: { rule: 'bg-held/40', count: 'text-held/80', badge: 'bg-held/5 text-held border-held/20' },
  description: {
    rule: 'bg-rule',
    count: 'text-slate/60',
    badge: 'bg-paper text-slate/60 border-rule',
  },
  removed: { rule: 'bg-rule', count: 'text-slate/60', badge: 'bg-paper text-slate/60 border-rule' },
};

// ── Layout ──────────────────────────────────────────────────────────────────

export function PageHeading({
  eyebrow,
  title,
  lede,
  aside,
}: {
  eyebrow?: ReactNode;
  title: string;
  lede?: ReactNode;
  aside?: ReactNode;
}) {
  return (
    <header className="mb-8 flex flex-col gap-4 border-b border-rule pb-6 sm:flex-row sm:items-end sm:justify-between">
      <div className="max-w-2xl">
        {eyebrow && <p className="label mb-2">{eyebrow}</p>}
        <h1 className="font-display text-3xl font-semibold leading-tight text-ink sm:text-4xl">
          {title}
        </h1>
        {lede && <p className="mt-3 text-sm leading-relaxed text-slate/80">{lede}</p>}
      </div>
      {aside && <div className="shrink-0">{aside}</div>}
    </header>
  );
}

export function Count({ value, unit, tone = '' }: { value: number; unit: string; tone?: string }) {
  return (
    <span className="inline-flex items-baseline gap-1.5">
      <span className={`font-display text-2xl font-semibold tabular-nums ${tone || 'text-ink'}`}>
        {value}
      </span>
      <span className="label">{unit}</span>
    </span>
  );
}

/**
 * An empty state that says what the emptiness means. A server with no
 * approved baseline is a normal, important, fail-closed state — not a fault —
 * so `tone` decides whether it reads as settled, held, or broken.
 */
export function EmptyState({
  tone = 'settled',
  title,
  children,
  action,
}: {
  tone?: 'settled' | 'held' | 'fault';
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  const border =
    tone === 'held' ? 'border-held/35' : tone === 'fault' ? 'border-fault/35' : 'border-rule';
  const bar = tone === 'held' ? 'bg-held' : tone === 'fault' ? 'bg-fault' : 'bg-rule';

  return (
    <div className={`relative overflow-hidden rounded-sm border bg-white px-6 py-7 ${border}`}>
      <span aria-hidden className={`absolute inset-y-0 left-0 w-[3px] ${bar}`} />
      <h3 className="font-display text-lg font-semibold text-ink">{title}</h3>
      <div className="mt-2 max-w-2xl space-y-2 text-sm leading-relaxed text-slate/80">
        {children}
      </div>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function Button({
  variant = 'quiet',
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'quiet' | 'danger';
}) {
  const styles = {
    primary: 'bg-slate text-paper hover:bg-ink disabled:bg-slate/40',
    quiet: 'border border-rule bg-white text-slate hover:border-slate/40 disabled:text-slate/40',
    danger: 'border border-fault/40 bg-white text-fault hover:bg-fault/5 disabled:text-fault/40',
  }[variant];

  return (
    <button
      type="button"
      {...props}
      className={`inline-flex items-center justify-center gap-2 rounded-sm px-4 py-2 font-sans text-sm font-medium transition-colors disabled:cursor-not-allowed ${styles} ${props.className ?? ''}`}
    />
  );
}

/** Skeleton for a list that is still loading. Still, not shimmering. */
export function Loading({ label }: { label: string }) {
  return (
    <p role="status" className="py-10 text-center text-sm text-slate/50">
      {label}
    </p>
  );
}
