import { defineConfig, devices } from "@playwright/test";
import * as os from "node:os";
import * as path from "node:path";

// Each project gets its own server and its own throwaway Postgres schema
// (e2e_*), which is dropped and re-created on start. Database credentials
// come from ../.env or the environment, exactly like the app.
const goBin = path.join(os.homedir(), ".local", "go", "bin");
// SCREENSHOT_VERSION (a pinned hash, from scripts/screenshots.sh) fixes the
// version the sidebar shows, so retaken screenshots change only with the pages.
const version = process.env.SCREENSHOT_VERSION ?? "";
if (version && !/^[0-9a-f]{7,40}$/.test(version)) throw new Error(`SCREENSHOT_VERSION ${version} is not a commit hash`);
const ldflags = version ? ` -ldflags "-X main.version=${version}"` : "";
function server(port: number, schema: string) {
  const binary = path.join("bin", `pitbox-${schema}`);
  return {
    command: `go build${ldflags} -o ${binary} ./cmd/pitbox && ./${binary} -reset-schema`,
    cwd: "..",
    url: `http://127.0.0.1:${port}/healthz`,
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: "ignore" as const,
    stderr: "pipe" as const,
    env: {
      PATH: `${goBin}:${process.env.PATH}`,
      HOST: "127.0.0.1",
      PORT: String(port),
      DB_SCHEMA: schema,
      APP_CURRENCY: "EUR",
      // The tests' own, whatever a local .env says (env wins over .env).
      APP_SKU_SEARCH_URL: "https://www.google.com/search?q={q}",
    },
  };
}

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 2, // one per project; each has its own server and schema
  forbidOnly: !!process.env.CI,
  // No retries: the tests run in order against one schema, so a retry of a
  // later test would start from a state the earlier ones already changed.
  retries: 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "desktop",
      use: { ...devices["Desktop Chrome"], baseURL: "http://127.0.0.1:18080", viewport: { width: 1360, height: 900 } },
    },
    {
      name: "mobile",
      use: { ...devices["Pixel 7"], baseURL: "http://127.0.0.1:18081" },
    },
  ],
  webServer: [server(18080, "e2e_desktop"), server(18081, "e2e_mobile")],
});
