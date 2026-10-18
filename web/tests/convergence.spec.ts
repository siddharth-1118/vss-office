import { test, expect } from '@playwright/test';
import { spawn, ChildProcess, execSync } from 'child_process';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';

test.describe('Two-tab collaboration convergence', () => {
  let serverProcess: ChildProcess;
  let tempDir: string;
  const SERVER_PORT = '8091';
  const SERVER_URL = `http://localhost:${SERVER_PORT}`;

  test.beforeAll(async () => {
    tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'vss-conv-test-'));
    const serverBinary = path.resolve('..', 'server', 'vss-server.exe');
    if (!fs.existsSync(serverBinary)) {
      throw new Error(`Server binary not found at ${serverBinary}`);
    }

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
    let up = false;
    for (let i = 0; i < maxAttempts; i++) {
      try {
        const res = await fetch(`${SERVER_URL}/healthz`);
        if (res.ok) {
          up = true;
          break;
        }
      } catch {
        // wait
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    if (!up) {
      throw new Error(`Server failed to boot on port ${SERVER_PORT}`);
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

  test('two tabs converge on cell edits and formula recalculation', async ({ browser }) => {
    // Register user via API
    const email = `test-${Date.now()}@test.local`;
    const password = 'hunter2hunter2';
    const regRes = await fetch(`${SERVER_URL}/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
    expect(regRes.status).toBe(201);

    // Login user via API to capture JWT
    const loginRes = await fetch(`${SERVER_URL}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
    expect(loginRes.status).toBe(200);
    const { token, user } = await loginRes.json();
    expect(token).toBeTruthy();

    // Create a doc via API
    const docRes = await fetch(`${SERVER_URL}/api/docs`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ name: 'Convergence Test Sheet', type: 'grid' }),
    });
    expect(docRes.status).toBe(201);
    const doc = await docRes.json();
    const docId = doc.id;
    expect(docId).toBeTruthy();

    // Open two browser contexts
    const contextA = await browser.newContext();
    const contextB = await browser.newContext();

    // Inject JWT and custom server port into localStorage
    const initStorage = (data: { token: string; user: any; port: string }) => {
      localStorage.setItem('vss_token', data.token);
      localStorage.setItem('vss_user', JSON.stringify(data.user));
      localStorage.setItem('vss_server_port', data.port);
    };

    await contextA.addInitScript(initStorage, { token, user, port: SERVER_PORT });
    await contextB.addInitScript(initStorage, { token, user, port: SERVER_PORT });

    const pageA = await contextA.newPage();
    const pageB = await contextB.newPage();

    // Both navigate to http://localhost:5173/docs/<docId>
    await pageA.goto(`/docs/${docId}`);
    await pageB.goto(`/docs/${docId}`);

    // Wait for connection on both tabs
    await expect(pageA.getByTestId('connection-status')).toHaveText('● connected', { timeout: 10000 });
    await expect(pageB.getByTestId('connection-status')).toHaveText('● connected', { timeout: 10000 });

    // Tab A types "1" into A1, presses Enter
    const cellA_A1 = pageA.getByTestId('cell-A1');
    await cellA_A1.click();
    const editorA_A1 = pageA.getByTestId('cell-editor');
    await editorA_A1.fill('1');
    await editorA_A1.press('Enter');

    // Assert tab B's A1 shows "1" within 2 seconds
    const cellB_A1 = pageB.getByTestId('cell-A1');
    await expect(cellB_A1, "Tab B did not receive Tab A's edit").toHaveText('1', { timeout: 2000 });

    // Tab B types "=A1+2" into B1, presses Enter
    const cellB_B1 = pageB.getByTestId('cell-B1');
    await cellB_B1.click();
    const editorB_B1 = pageB.getByTestId('cell-editor');
    await editorB_B1.fill('=A1+2');
    await editorB_B1.press('Enter');

    // Assert both tabs show "3" in B1 within 2 seconds
    const cellA_B1 = pageA.getByTestId('cell-B1');
    await expect(cellA_B1, "Tab A did not receive formula evaluation").toHaveText('3', { timeout: 2000 });
    await expect(cellB_B1, "Tab B did not receive formula evaluation").toHaveText('3', { timeout: 2000 });

    // Close both
    await contextA.close();
    await contextB.close();
  });
});
