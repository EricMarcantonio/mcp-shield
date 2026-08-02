import type { Config } from 'tailwindcss';

// Tailwind is here for layout only: flex, grid, gaps, breakpoints. Colour,
// type, radius, spacing and every component surface come from the design
// system in src/ds/organic.css, addressed through its CSS variables and
// classes. There is deliberately no palette in this file — a second copy of
// the tokens would be a second source of truth, and the two would drift.
//
// Preflight is off because organic.css already carries a reset and a type
// scale, and letting both reset the page means fighting over `h1`.
// src/index.css supplies the one preflight rule Tailwind's own utilities
// depend on (border width/style/colour defaults).
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  corePlugins: { preflight: false },
  theme: {},
  plugins: [],
} satisfies Config;
