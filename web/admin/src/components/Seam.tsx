/**
 * The seam.
 *
 * The logo is a shield split by a vertical line into two halves: the baseline
 * that was approved, and what the upstream is advertising now. This component
 * is that figure made operational. One continuous rule runs the full height of
 * the card; the approved manifest sits left of it, the proposed manifest right
 * of it, and every difference is a row that crosses it.
 *
 * Approving closes the seam. The halves meet, the rule settles out of `signal`
 * into `slate`, and the view stops being a question. It is the only
 * orchestrated motion in the product, and `prefers-reduced-motion` removes it.
 */

import type { ReactNode } from 'react';
import type { ChangeGroup, ChangeItem } from '../api/derive';
import type { ManifestState } from '../api/types';
import { AdmittedMark, HeldMark, Identity, KIND_WEIGHT } from './primitives';

export type SeamPhase = 'open' | 'closing' | 'closed';

interface SeamProps {
  phase: SeamPhase;
  /** Live while the manifest is undecided; settled once it is not. */
  live: boolean;
  left: ReactNode;
  right: ReactNode;
  children: ReactNode;
}

export function Seam({ phase, live, left, right, children }: SeamProps) {
  const closing = phase !== 'open';
  const ruleTone = live && !closing ? 'bg-signal' : 'bg-slate';

  return (
    <section className="card relative overflow-hidden" aria-live="polite">
      {/* One rule, full height. This is the seam. */}
      <span
        aria-hidden
        className={`absolute inset-y-0 left-1/2 hidden w-[2px] -translate-x-1/2 transition-colors duration-500 sm:block ${ruleTone} ${
          closing ? 'motion-safe:animate-seam-settle' : ''
        }`}
      />

      <header className="grid grid-cols-1 border-b border-rule sm:grid-cols-2">
        <SeamHalf side="left" closing={closing} title="Approved baseline">
          {left}
        </SeamHalf>
        <SeamHalf side="right" closing={closing} title="Advertised now">
          {right}
        </SeamHalf>
      </header>

      <div className="relative">{children}</div>
    </section>
  );
}

function SeamHalf({
  side,
  title,
  closing,
  children,
}: {
  side: 'left' | 'right';
  title: string;
  closing: boolean;
  children: ReactNode;
}) {
  const edge = side === 'left' ? 'pl-5 pr-6' : 'border-t border-rule pl-5 pr-5 sm:border-t-0 sm:pl-6';
  const motion = closing
    ? side === 'left'
      ? 'motion-safe:animate-seam-close-left'
      : 'motion-safe:animate-seam-close-right'
    : '';

  return (
    <div className={`py-4 ${edge} ${motion}`}>
      <p className="label">{title}</p>
      <div className="mt-2">{children}</div>
    </div>
  );
}

/**
 * One class of change, drawn with a weight that argues for its consequence.
 * A changed input schema gets a solid amber rule; a changed description gets a
 * hairline and quiet type. The reader should be able to tell them apart before
 * reading a word.
 */
export function ChangeSection({
  group,
  closing,
  state,
}: {
  group: ChangeGroup;
  closing: boolean;
  state: ManifestState;
}) {
  const weight = KIND_WEIGHT[group.kind];

  return (
    <section className="border-b border-rule last:border-b-0">
      <div className="relative py-5">
        <span aria-hidden className={`absolute inset-y-0 left-0 w-[3px] ${weight.rule}`} />

        {/* Our commentary stays left of the seam so the rule is never broken
            by text running across it. */}
        <div className="px-5 sm:max-w-[calc(50%-1.5rem)]">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h3 className="font-display text-base font-semibold text-ink">{group.title}</h3>
            <span className={`font-display text-sm font-semibold tabular-nums ${weight.count}`}>
              {group.items.length}
            </span>
          </div>
          <p className="mt-1 text-sm leading-relaxed text-slate/75">{group.note}</p>
        </div>

        <ul className="mt-4 divide-y divide-rule border-t border-rule">
          {group.items.map((item) => (
            <ChangeRow
              key={`${item.domain}:${item.identity}`}
              item={item}
              closing={closing}
              state={state}
            />
          ))}
        </ul>
      </div>
    </section>
  );
}

/**
 * A row that crosses the seam. What existed in the baseline sits left, what
 * the upstream is advertising sits right, and the gap between them is the
 * decision. Both halves use the same two-column grid as the header, so the
 * seam is a single unbroken line from the top of the card to the bottom.
 */
function ChangeRow({
  item,
  closing,
  state,
}: {
  item: ChangeItem;
  closing: boolean;
  state: ManifestState;
}) {
  const inBaseline = item.kind !== 'added';
  const inProposed = item.kind !== 'removed';

  return (
    <li className="grid grid-cols-1 items-center sm:grid-cols-2">
      {/* On a narrow screen the two halves stack, and an empty half becomes a
          stray dash on its own line. The group heading already says the item
          is new or dropped, so the placeholder only appears beside its
          counterpart. */}
      <span
        className={`min-w-0 truncate py-2.5 pl-5 pr-6 ${inBaseline ? '' : 'hidden sm:block'} ${
          closing ? 'motion-safe:animate-seam-close-left' : ''
        }`}
      >
        {inBaseline ? (
          <Identity className="text-slate">{item.identity}</Identity>
        ) : (
          <Absent label="not in the approved baseline" />
        )}
      </span>

      <span
        className={`min-w-0 items-center justify-between gap-3 py-2.5 pl-5 pr-5 sm:pl-6 ${
          inProposed ? 'flex' : 'hidden sm:flex'
        } ${closing ? 'motion-safe:animate-seam-close-right' : ''}`}
      >
        <span className="min-w-0 truncate">
          {inProposed ? (
            <Identity>{item.identity}</Identity>
          ) : (
            <Absent label="no longer advertised" />
          )}
        </span>
        <Mark item={item} state={state} />
      </span>

      {/* Keep the mark visible on mobile for a dropped item, whose only cell
          is the hidden one. */}
      {!inProposed && (
        <span className="flex justify-end px-5 pb-2.5 sm:hidden">
          <Mark item={item} state={state} />
        </span>
      )}
    </li>
  );
}

/**
 * What the gate is doing with this item *now*. A row does not stay "withheld"
 * once the manifest carrying it has been approved — approving is precisely the
 * act that admits it, and leaving the amber up would say the opposite of what
 * just happened.
 */
function Mark({ item, state }: { item: ChangeItem; state: ManifestState }) {
  if (!item.withheld) return <AdmittedMark label="Dropped" />;

  switch (state) {
    case 'APPROVED':
      return <AdmittedMark />;
    case 'PENDING':
      return <HeldMark />;
    case 'REJECTED':
      return <HeldMark label="Refused" />;
    case 'SUPERSEDED':
      return <AdmittedMark label="Superseded" />;
  }
}

function Absent({ label }: { label: string }) {
  return (
    <span aria-label={label} className="font-mono text-sm text-slate/25">
      —
    </span>
  );
}
