/**
 * @klarlabs-studio/glossa-react's own test files, run against React 18.3 instead of 19.
 *
 * A package of its own because pnpm resolves `react-dom`'s peer `react` from
 * the depending package: in @klarlabs-studio/glossa-react an aliased `react-dom@18` would
 * still get React 19. Here both are 18.3, and the aliases point the tests'
 * and the sources' `react` / `react-dom` imports at them.
 */
import { createRequire } from "node:module";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const require = createRequire(import.meta.url);
const dir = (name: string) => dirname(require.resolve(`${name}/package.json`));

export default defineConfig({
  root: fileURLToPath(new URL("..", import.meta.url)),
  resolve: {
    alias: [
      { find: /^react$/, replacement: dir("react") },
      { find: /^react-dom(\/.*)?$/, replacement: `${dir("react-dom")}$1` },
    ],
  },
  test: {
    name: "react-18",
    include: ["src/**/*.test.ts", "react-18/*.test.ts"],
    // As in @klarlabs-studio/glossa-react: plain Node, and jsdom where a test file opts in.
    environment: "node",
  },
});
