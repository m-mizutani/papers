import { defineConfig, devices } from '@playwright/test'

// End-to-end tests run the real server (in-memory storage, sign-in off,
// the example workspaces) with the built UI embedded, and drive it in
// Chromium. Build the UI first (`pnpm build`). PAPERS_BIN runs a prebuilt
// server binary instead of `go run`.
const port = Number(process.env.E2E_PORT ?? 18090)
const server = process.env.PAPERS_BIN ?? 'go run .'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    locale: 'en-US',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        locale: 'en-US',
        // A locally installed Chrome is used when CHROME_PATH is set.
        launchOptions: process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {},
      },
    },
  ],
  webServer: {
    command: `${server} serve --no-authn --addr 127.0.0.1:${port} --base-url http://127.0.0.1:${port} --config examples/config --global-config examples/global.toml --job-step 2`,
    cwd: '..',
    url: `http://127.0.0.1:${port}/healthz`,
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: 'ignore',
    stderr: 'pipe',
  },
})
