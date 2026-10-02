import { defineConfig } from 'vitest/config'
import base from './vitest.config'

/**
 * Timing budgets, run by `npm run bench:layout` and never by `npm test`.
 *
 * A wall-clock assertion is a fact about the machine as much as the code: in
 * the normal suite it flaked whenever other test files loaded the same CPU.
 * Budgets live in `*.bench.ts`, run one file at a time, and are checked on
 * purpose — before a release, or after touching the layout.
 */
// Not mergeConfig: it concatenates `include`, which would run every test too.
export default defineConfig({
  ...base,
  test: {
    ...base.test,
    include: ['src/**/*.bench.ts'],
    fileParallelism: false,
  },
})
