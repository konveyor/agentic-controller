import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      // Consume the client core straight from source (no build step needed).
      // tsconfig.app.json "paths" mirrors this so tsc agrees with Vite.
      "@konveyor/agentic-client": path.resolve(here, "../packages/agentic-client/src"),
    },
  },
  server: {
    // The core package lives outside this Vite root — allow the repo root.
    fs: { allow: [path.resolve(here, "..")] },
  },
});
