/**
 * Who this console records decisions as.
 *
 * There is no authentication here and none is planned in this layer — the
 * gateway's approval API is meant to be fronted by Keycloak. What the API does
 * require is a `username` on every approve and reject, and it refuses to
 * invent one, because a decision that cannot be attributed is not a record.
 *
 * So this provider supplies an operator name, from configuration or from the
 * operator themselves, and the console is careful in every place it appears to
 * say that the name is *recorded*, not verified. When a real identity provider
 * arrives, `operator` comes from the token's claims and
 * `setAuthHeaderProvider` (api/client.ts) starts returning the bearer header.
 * Nothing else moves.
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { configuredOperator } from '../lib/config';

const STORAGE_KEY = 'mcp-shield.operator';

interface AuthValue {
  /** The name written into the approvals table. Empty until one is set. */
  operator: string;
  setOperator: (name: string) => void;
  /** True when configuration supplied the name and it should not be edited. */
  fixed: boolean;
}

const AuthContext = createContext<AuthValue | null>(null);

function initialOperator(): string {
  if (configuredOperator) return configuredOperator;
  try {
    return window.localStorage.getItem(STORAGE_KEY) ?? '';
  } catch {
    return '';
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const fixed = configuredOperator !== '';
  const [operator, setOperatorState] = useState(initialOperator);

  useEffect(() => {
    if (fixed) return;
    try {
      if (operator) window.localStorage.setItem(STORAGE_KEY, operator);
      else window.localStorage.removeItem(STORAGE_KEY);
    } catch {
      // A browser refusing storage costs the operator a retype, nothing more.
    }
  }, [operator, fixed]);

  const setOperator = useCallback(
    (name: string) => {
      if (!fixed) setOperatorState(name.trim());
    },
    [fixed],
  );

  const value = useMemo<AuthValue>(
    () => ({ operator, setOperator, fixed }),
    [operator, setOperator, fixed],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthValue {
  const value = useContext(AuthContext);
  if (!value) throw new Error('useAuth called outside AuthProvider');
  return value;
}
