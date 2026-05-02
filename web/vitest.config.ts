// Vitest config — kept separate from vite.config.ts so the dev/build
// pipeline doesn't drag in test-only deps. Vitest picks this up
// automatically when run from the web/ directory.

import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Default include matches *.test.ts under src/. We're starting with
    // lib/ tests only; component tests get added when there's UI logic
    // worth testing in isolation (PR4 inline editor state machine).
    include: ["src/**/*.test.ts"],
    environment: "node",
    globals: false,
  },
});
