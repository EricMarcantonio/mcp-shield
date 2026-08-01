/**
 * Every read the console performs, in one place, so caching and invalidation
 * are decisions rather than accidents.
 *
 * The polling intervals are deliberate: a pending manifest means traffic is
 * being withheld right now, so the queue refreshes on its own; a decision
 * ledger does not move unless somebody moves it.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import * as api from './client';
import { baselineChain, buildServerLedgers, replayCapabilities } from './derive';
import type { Reconstruction, ServerLedger } from './derive';
import type { Manifest } from './types';

const PENDING_POLL_MS = 15_000;
const HEALTH_POLL_MS = 20_000;

export const keys = {
  servers: ['servers'] as const,
  pending: ['manifests', 'pending'] as const,
  manifestIndex: ['manifests', 'index'] as const,
  manifest: (id: number) => ['manifests', id] as const,
  diff: (id: number) => ['manifests', id, 'diff'] as const,
  approvals: (id: number) => ['manifests', id, 'approvals'] as const,
  contents: (id: number) => ['manifests', id, 'contents'] as const,
  failedNotifications: ['notifications', 'failed'] as const,
  health: ['health'] as const,
};

export function usePending() {
  return useQuery({
    queryKey: keys.pending,
    queryFn: api.listPendingManifests,
    refetchInterval: PENDING_POLL_MS,
  });
}

export function useHealth() {
  return useQuery({
    queryKey: keys.health,
    queryFn: api.getHealth,
    refetchInterval: HEALTH_POLL_MS,
    retry: false,
  });
}

export function useManifest(id: number | null) {
  return useQuery({
    queryKey: keys.manifest(id ?? -1),
    queryFn: () => api.getManifest(id as number),
    enabled: id !== null,
  });
}

export function useManifestDiff(id: number | null) {
  return useQuery({
    queryKey: keys.diff(id ?? -1),
    queryFn: () => api.getManifestDiff(id as number),
    enabled: id !== null,
  });
}

export function useManifestApprovals(id: number | null) {
  return useQuery({
    queryKey: keys.approvals(id ?? -1),
    queryFn: () => api.getManifestApprovals(id as number),
    enabled: id !== null,
  });
}

export function useFailedNotifications() {
  return useQuery({
    queryKey: keys.failedNotifications,
    queryFn: api.listFailedNotifications,
  });
}

export function useManifestIndex() {
  return useQuery({
    queryKey: keys.manifestIndex,
    queryFn: api.listAllManifests,
    // The fallback path costs one request per manifest; do not repeat it on
    // every focus change.
    staleTime: 30_000,
  });
}

export interface ServersView {
  ledgers: ServerLedger[];
  /** True when the manifest index had to be reconstructed by probing ids. */
  probed: boolean;
}

export function useServers() {
  const servers = useQuery({ queryKey: keys.servers, queryFn: api.listServers });
  const index = useManifestIndex();

  const view: ServersView | undefined =
    servers.data && index.data
      ? {
          ledgers: buildServerLedgers(servers.data, index.data.manifests),
          probed: index.data.probed,
        }
      : undefined;

  return {
    data: view,
    isPending: servers.isPending || index.isPending,
    error: servers.error ?? index.error,
    refetch: () => {
      void servers.refetch();
      void index.refetch();
    },
  };
}

/**
 * The capability set a server's approved manifest admits.
 *
 * Prefers a served manifest body; falls back to replaying the stored diffs
 * along the baseline chain, and reports which of the two it did so the view
 * can say so rather than imply a provenance it does not have.
 */
export function useApprovedCapabilities(ledger: ServerLedger | null) {
  const chain = ledger ? baselineChain(ledger) : [];
  const approvedId = ledger?.approved?.id ?? null;

  return useQuery({
    queryKey: ['capabilities', approvedId, chain.map((m) => m.id).join(',')],
    enabled: approvedId !== null,
    staleTime: 60_000,
    queryFn: async (): Promise<{
      source: 'served' | 'replayed';
      reconstruction: Reconstruction;
    }> => {
      const served = await api.getManifestContents(approvedId as number);
      if (served) {
        return {
          source: 'served',
          reconstruction: {
            sound: true,
            chainLength: chain.length,
            set: {
              tools: served.tools.map((t) => t.name).sort(),
              prompts: served.prompts.map((p) => p.name).sort(),
              resources: served.resources.map((r) => r.uri).sort(),
            },
          },
        };
      }

      const withDiffs = await Promise.all(
        chain.map(async (manifest: Manifest) => ({
          manifest,
          diff: await api.getManifestDiff(manifest.id),
        })),
      );
      return { source: 'replayed', reconstruction: replayCapabilities(withDiffs) };
    },
  });
}

export type DecisionKind = 'approve' | 'reject';

export function useDecision(kind: DecisionKind) {
  const client = useQueryClient();

  return useMutation({
    mutationFn: ({ id, username, reason }: { id: number; username: string; reason: string }) =>
      kind === 'approve'
        ? api.approveManifest(id, username, reason)
        : api.rejectManifest(id, username, reason),
    onSuccess: (_data, { id }) => {
      void client.invalidateQueries({ queryKey: keys.pending });
      void client.invalidateQueries({ queryKey: keys.manifestIndex });
      void client.invalidateQueries({ queryKey: keys.manifest(id) });
      void client.invalidateQueries({ queryKey: keys.approvals(id) });
      void client.invalidateQueries({ queryKey: ['capabilities'] });
    },
  });
}
