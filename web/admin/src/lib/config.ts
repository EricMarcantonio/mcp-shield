/**
 * Where the approval API lives, and who this console says it is.
 *
 * Resolution order, most specific first:
 *   1. `window.__MCP_SHIELD__` — written at container start from $API_BASE_URL
 *      / $OPERATOR_NAME, so a built image is repointable without a rebuild.
 *   2. `VITE_API_BASE_URL` / `VITE_OPERATOR_NAME` — baked at build time.
 *   3. Same origin, and an empty operator name.
 *
 * Nothing here hardcodes localhost:8081. In dev, same-origin resolves through
 * the Vite proxy in vite.config.ts.
 */

declare global {
  interface Window {
    __MCP_SHIELD__?: {
      apiBaseUrl?: string;
      operator?: string;
    };
  }
}

function runtime(): NonNullable<Window['__MCP_SHIELD__']> {
  return window.__MCP_SHIELD__ ?? {};
}

/** Trailing slashes are stripped so paths can be concatenated blindly. */
function trimTrailingSlash(url: string): string {
  return url.replace(/\/+$/, '');
}

export const apiBaseUrl: string = trimTrailingSlash(
  runtime().apiBaseUrl?.trim() || import.meta.env.VITE_API_BASE_URL?.trim() || '',
);

export const configuredOperator: string =
  runtime().operator?.trim() || import.meta.env.VITE_OPERATOR_NAME?.trim() || '';
