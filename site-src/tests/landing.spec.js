const { test, expect } = require('@playwright/test');

const localAssets = [
  '/assets/site.css',
  '/assets/site.js',
  '/assets/logo.png',
  '/assets/tui-demo-poster.webp',
  '/assets/cli-demo-poster.webp',
  '/assets/tui-demo.mp4',
  '/assets/cli-demo.mp4',
];

async function installMediaSpies(page) {
  await page.addInitScript(() => {
    window.__mediaCalls = { play: [], pause: [] };
    window.__mediaPaused = {};
    HTMLMediaElement.prototype.play = function play() {
      const panelId = this.closest('[role="tabpanel"]')?.id;
      window.__mediaCalls.play.push(panelId);
      window.__mediaPaused[panelId] = false;
      return Promise.resolve();
    };
    HTMLMediaElement.prototype.pause = function pause() {
      const panelId = this.closest('[role="tabpanel"]')?.id;
      window.__mediaCalls.pause.push(panelId);
      window.__mediaPaused[panelId] = true;
    };
  });
}

test('loads the static landing page and all local assets', async ({ page, request }) => {
  const consoleMessages = [];
  const requestedURLs = [];
  page.on('console', (message) => consoleMessages.push(message.text()));
  page.on('request', (req) => requestedURLs.push(req.url()));

  await page.goto('/?noanim');

  await expect(page).toHaveTitle('TidyMyMac — macOS Storage Cleanup Tool');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Scan. Review. Reclaim.');
  await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(22, 21, 26)');
  await expect(page.getByRole('heading', { level: 1 })).toHaveCSS('font-weight', '800');
  expect(requestedURLs.some((url) => url.includes('cdn.tailwindcss.com'))).toBe(false);
  expect(consoleMessages.some((message) => /cdn\.tailwindcss\.com|tailwind.*production/i.test(message))).toBe(false);

  for (const asset of localAssets) {
    const response = await request.get(asset);
    expect(response.status(), `${asset} should respond successfully`).toBe(200);
  }
});

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
  { width: 320, height: 800 },
]) {
  test(`fits the page at ${viewport.width}x${viewport.height}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.goto('/?noanim');
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    const tablist = page.getByRole('tablist', { name: 'Install method' });
    await expect(tablist).toBeVisible();

    const terminal = page.locator('#install .term');
    await terminal.scrollIntoViewIfNeeded();
    const terminalBox = await terminal.boundingBox();
    expect(terminalBox).not.toBeNull();

    const tabs = await tablist.getByRole('tab').all();
    expect(tabs).toHaveLength(4);
    for (const tab of tabs) {
      const box = await tab.boundingBox();
      expect(box).not.toBeNull();
      expect(box.x).toBeGreaterThanOrEqual(terminalBox.x);
      expect(box.x + box.width).toBeLessThanOrEqual(terminalBox.x + terminalBox.width);
      expect(box.y).toBeGreaterThanOrEqual(terminalBox.y);
      expect(box.y + box.height).toBeLessThanOrEqual(terminalBox.y + terminalBox.height);
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(viewport.width);
      expect(box.y).toBeGreaterThanOrEqual(0);
      expect(box.y + box.height).toBeLessThanOrEqual(viewport.height);
    }
  });
}

test('tabs support click and roving keyboard activation', async ({ page }) => {
  await page.goto('/?noanim');

  const demoTui = page.getByRole('tab', { name: 'interactive TUI' });
  const demoCli = page.getByRole('tab', { name: 'CLI', exact: true });
  await demoCli.click();
  await expect(demoCli).toHaveAttribute('aria-selected', 'true');
  await expect(demoCli).toHaveAttribute('tabindex', '0');
  await expect(demoTui).toHaveAttribute('aria-selected', 'false');
  await expect(demoTui).toHaveAttribute('tabindex', '-1');
  await expect(page.locator('#panel-cli')).toBeVisible();
  await expect(page.locator('#panel-tui')).toBeHidden();

  const curl = page.getByRole('tab', { name: 'curl', exact: true });
  const brew = page.getByRole('tab', { name: 'homebrew', exact: true });
  const goInstall = page.getByRole('tab', { name: 'go install', exact: true });
  const source = page.getByRole('tab', { name: 'source', exact: true });

  await curl.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(source).toBeFocused();
  await expect(source).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator('#install-source')).toBeVisible();
  await expect(page.locator('#install-curl')).toBeHidden();

  await page.keyboard.press('ArrowRight');
  await expect(curl).toBeFocused();
  await page.keyboard.press('End');
  await expect(source).toBeFocused();
  await page.keyboard.press('Home');
  await expect(curl).toBeFocused();
  await page.keyboard.press('ArrowRight');
  await expect(brew).toBeFocused();
  await expect(brew).toHaveAttribute('aria-selected', 'true');
  await expect(goInstall).toHaveAttribute('aria-selected', 'false');
});

test('only the active demo video plays and switching tabs pauses the previous video', async ({ page }) => {
  await installMediaSpies(page);
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('/?noanim');

  const tuiVideo = page.locator('#panel-tui video');
  const cliVideo = page.locator('#panel-cli video');
  for (const video of [tuiVideo, cliVideo]) {
    await expect(video).toHaveAttribute('muted', '');
    await expect(video).toHaveAttribute('loop', '');
    await expect(video).toHaveAttribute('playsinline', '');
    await expect(video).toHaveAttribute('controls', '');
    const descriptionId = await video.getAttribute('aria-describedby');
    expect(descriptionId).toBeTruthy();
    const description = page.locator(`#${descriptionId}`);
    await expect(description).toHaveCount(1);
    expect((await description.textContent()).trim().length).toBeGreaterThan(40);
  }
  await expect(tuiVideo).toHaveAttribute('preload', 'metadata');
  await expect(cliVideo).toHaveAttribute('preload', 'none');
  await expect.poll(() => page.evaluate(() => window.__mediaCalls.play)).toContain('panel-tui');
  expect(await page.evaluate(() => window.__mediaCalls.play)).not.toContain('panel-cli');
  expect(await page.evaluate(() => window.__mediaCalls.pause)).toContain('panel-cli');

  await page.getByRole('tab', { name: 'CLI', exact: true }).click();
  await expect.poll(() => page.evaluate(() => window.__mediaCalls.play)).toContain('panel-cli');
  expect(await page.evaluate(() => window.__mediaCalls.pause)).toContain('panel-tui');
});

test('install tabs do not restart a manually paused demo video', async ({ page }) => {
  await installMediaSpies(page);
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('/?noanim');

  const tuiVideo = page.locator('#panel-tui video');
  await expect.poll(() => page.evaluate(() => window.__mediaCalls.play)).toContain('panel-tui');
  await tuiVideo.evaluate((video) => video.pause());
  const playsBeforeInstallTab = await page.evaluate(() => window.__mediaCalls.play.length);
  expect(await page.evaluate(() => window.__mediaPaused['panel-tui'])).toBe(true);

  await page.getByRole('tab', { name: 'homebrew', exact: true }).click();

  expect(await page.evaluate(() => window.__mediaCalls.play.length)).toBe(playsBeforeInstallTab);
  expect(await page.evaluate(() => window.__mediaPaused['panel-tui'])).toBe(true);
});

test('reduced motion pauses both videos and never requests playback', async ({ page }) => {
  await installMediaSpies(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/?noanim');
  await page.getByRole('tab', { name: 'CLI', exact: true }).click();

  const calls = await page.evaluate(() => window.__mediaCalls);
  expect(calls.play).toEqual([]);
  expect(calls.pause).toContain('panel-tui');
  expect(calls.pause).toContain('panel-cli');
});

test('copy buttons write the active command without the prompt', async ({ page }) => {
  await page.goto('/?noanim');

  await page.getByRole('button', { name: 'Copy install command' }).first().click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(
    'curl -fsSL https://raw.githubusercontent.com/viniciussouzao/tidymymac/main/install.sh | sh',
  );

  await page.getByRole('tab', { name: 'homebrew', exact: true }).click();
  const installCopy = page.locator('#install .copy-btn');
  await installCopy.click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(
    'brew install viniciussouzao/tap/tidymymac',
  );
  await expect(installCopy).toHaveText('copied ✓');
});
