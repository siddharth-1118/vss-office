import { test, expect } from '@playwright/test';
import { spawn, ChildProcess, execSync } from 'child_process';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';

test.describe('Auth and Session Expiration', () => {
  let serverProcess: ChildProcess;
  let tempDir: string;
  const SERVER_PORT = '8092';
  const SERVER_URL = `http://localhost:${SERVER_PORT}`;

  test.beforeAll(async () => {
    tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'vss-auth-test-'));
    const serverBinary = path.resolve('..', 'server', 'vss-server.exe');

    serverProcess = spawn(
      serverBinary,
      [
        '-addr', `:${SERVER_PORT}`,
        '-jwt-secret', 'test-secret-please-change',
        '-data-dir', tempDir,
      ],
      { stdio: 'ignore' }
    );

    const maxAttempts = 50;
    for (let i = 0; i < maxAttempts; i++) {
      try {
        const res = await fetch(`${SERVER_URL}/healthz`);
        if (res.ok) break;
      } catch {}
      await new Promise((r) => setTimeout(r, 100));
    }
  });

  test.afterAll(async () => {
    if (serverProcess && serverProcess.pid) {
      try {
        execSync(`taskkill /pid ${serverProcess.pid} /T /F`);
      } catch {
        try {
          serverProcess.kill('SIGTERM');
        } catch {}
      }
    }
    if (tempDir && fs.existsSync(tempDir)) {
      try {
        fs.rmSync(tempDir, { recursive: true, force: true });
      } catch {}
    }
  });

  test('redirects to /login when token is expired or invalid on /docs', async ({ browser }) => {
    // 1. Get a valid JWT first
    const email = `auth-test-${Date.now()}@test.local`;
    const password = 'hunter2hunter2';
    await fetch(`${SERVER_URL}/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });

    const loginRes = await fetch(`${SERVER_URL}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
    const { token, user } = await loginRes.json();
    expect(token).toBeTruthy();

    const context = await browser.newContext();

    // Route /api and /auth to test server port
    await context.route('**/api/**', async (route) => {
      const url = route.request().url().replace(/^http:\/\/localhost:\d+/, SERVER_URL);
      const res = await context.request.fetch(route.request(), { url });
      await route.fulfill({ response: res });
    });
    await context.route('**/auth/**', async (route) => {
      const url = route.request().url().replace(/^http:\/\/localhost:\d+/, SERVER_URL);
      const res = await context.request.fetch(route.request(), { url });
      await route.fulfill({ response: res });
    });

    // 2. Manually expire it by setting localStorage token to "bad"
    await context.addInitScript((data: { u: any }) => {
      localStorage.setItem('vss_token', 'bad');
      localStorage.setItem('vss_user', JSON.stringify(data.u));
    }, { u: user });

    const page = await context.newPage();

    // 3. Navigate to /docs
    await page.goto('/docs');

    // 4. Assert redirect to /login within 2 seconds
    await expect(page, "Expected redirect to /login on 401 unauthorized").toHaveURL(/\/login/, { timeout: 2000 });

    await context.close();
  });
});
