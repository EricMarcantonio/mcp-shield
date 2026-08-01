import type { Config } from 'tailwindcss';

// The palette is derived from assets/logo.svg: a shield split by a vertical
// seam into a settled slate half and a live cyan half. Every colour used in
// the product is named here; no component spells out a hex value.
//
// `held` is amber and deliberately not red. A withheld capability is the
// gateway working exactly as designed, and colouring correct behaviour red
// trains an operator to read the product's whole purpose as a fault. Red is
// reserved for `fault` — a dead upstream, a notification that never landed.
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        ink: '#14283D', // primary text
        slate: '#1F3349', // the logo's dark half: structure, settled state
        signal: '#069EB9', // the logo's cyan: pending and live things only
        paper: '#F7F8F6', // page ground
        rule: '#D8DEE3', // hairlines, borders
        held: '#B45309', // withheld capabilities
        fault: '#A81E1E', // actual failures only
      },
      fontFamily: {
        // mono means machine-truth, sans means we wrote it.
        display: ['"IBM Plex Sans Condensed"', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        sans: ['"IBM Plex Sans"', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: ['"IBM Plex Mono"', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      fontSize: {
        micro: ['0.6875rem', { lineHeight: '1rem', letterSpacing: '0.08em' }],
      },
      boxShadow: {
        card: '0 1px 0 0 #D8DEE3',
        lift: '0 1px 2px 0 rgb(20 40 61 / 0.06), 0 8px 24px -12px rgb(20 40 61 / 0.18)',
      },
      keyframes: {
        // The one orchestrated motion in the product: approving closes the
        // seam. The two halves meet and the row settles out of signal.
        'seam-close-left': {
          '0%': { transform: 'translateX(0)' },
          '100%': { transform: 'translateX(0.75rem)' },
        },
        'seam-close-right': {
          '0%': { transform: 'translateX(0)' },
          '100%': { transform: 'translateX(-0.75rem)' },
        },
        'seam-settle': {
          '0%': { backgroundColor: '#069EB9', transform: 'scaleY(1)' },
          '55%': { backgroundColor: '#069EB9', transform: 'scaleY(1.06)' },
          '100%': { backgroundColor: '#1F3349', transform: 'scaleY(1)' },
        },
        'rise-in': {
          '0%': { opacity: '0', transform: 'translateY(0.25rem)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
      animation: {
        'seam-close-left': 'seam-close-left 620ms cubic-bezier(0.22, 1, 0.36, 1) forwards',
        'seam-close-right': 'seam-close-right 620ms cubic-bezier(0.22, 1, 0.36, 1) forwards',
        'seam-settle': 'seam-settle 620ms cubic-bezier(0.22, 1, 0.36, 1) forwards',
        'rise-in': 'rise-in 240ms ease-out both',
      },
    },
  },
  plugins: [],
} satisfies Config;
