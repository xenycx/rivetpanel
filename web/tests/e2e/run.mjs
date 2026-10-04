// Browser checks for the core workflows, run against a throwaway panel.
//
//   npm run test:e2e              (after `make build`; uses ../bin/rivetpanel)
//   RIVET_BIN=/path/rivetpanel CHROMIUM=/path/chrome npm run test:e2e
//
// The panel runs without Docker (RIVET_RUNNER_MODE=none) in a temporary
// directory with generated test accounts, so nothing touches real data.
// Screenshots of each page at phone, tablet and desktop size are written to
// tests/e2e/out/ for visual review.
import { chromium } from 'playwright-core';
import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, existsSync } from 'node:fs';
import { tmpdir, homedir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomBytes } from 'node:crypto';
import net from 'node:net';

const here = dirname(fileURLToPath(import.meta.url));
const BIN = resolve(process.env.RIVET_BIN ?? join(here, '../../../bin/rivetpanel'));
const CHROME = process.env.CHROMIUM ?? join(homedir(), '.cache/ms-playwright/chromium-1243/chrome-linux64/chrome');
const OUT = join(here, 'out');
if (!existsSync(BIN)) throw new Error(`panel binary not found at ${BIN}; run make build first`);

const results = [];
function check(name, ok, detail = '') {
	results.push({ name, ok, detail });
	console.log(`${ok ? 'ok  ' : 'FAIL'}  ${name}${detail ? `  (${detail})` : ''}`);
}
async function step(name, fn) {
	try {
		await fn();
		check(name, true);
	} catch (e) {
		check(name, false, String(e?.message ?? e).split('\n')[0]);
	}
}
const expect = (cond, msg) => {
	if (!cond) throw new Error(msg);
};

function freePort() {
	return new Promise((res) => {
		const s = net.createServer();
		s.listen(0, '127.0.0.1', () => {
			const p = s.address().port;
			s.close(() => res(p));
		});
	});
}

// ---- throwaway panel ----
const dir = mkdtempSync(join(tmpdir(), 'rivetpanel-e2e-'));
const port = await freePort();
const sitesPort = await freePort();
const base = `http://127.0.0.1:${port}`;
const env = { ...process.env, RIVET_ENV: 'development', RIVET_LISTEN: `127.0.0.1:${port}`, RIVET_RUNNER_MODE: 'none', RIVET_BACKUP_INTERVAL: '0',
	RIVET_SITES_LISTEN: `127.0.0.1:${sitesPort}`, RIVET_SITES_BASE_URL: `http://localhost:${sitesPort}` };
const adminPw = randomBytes(12).toString('base64url');
const userPw = randomBytes(12).toString('base64url');
execFileSync(BIN, ['create-admin', 'admin@e2e.test'], { cwd: dir, env: { ...env, RIVET_ADMIN_PASSWORD: adminPw }, stdio: 'ignore' });
const panel = spawn(BIN, [], { cwd: dir, env, stdio: ['ignore', 'ignore', 'pipe'] });
let panelLog = '';
panel.stderr.on('data', (d) => (panelLog += d));
for (let i = 0; i < 100; i++) {
	try {
		if ((await fetch(base + '/api/v1/healthz')).ok) break;
	} catch {
		/* not up yet */
	}
	await new Promise((r) => setTimeout(r, 100));
}

// API helper with its own session (for setup steps that are not under test).
async function apiSession(email, password) {
	const r = await fetch(base + '/api/v1/auth/login', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ email, password }) });
	const cookie = r.headers.get('set-cookie').split(';')[0];
	const { csrf_token } = await r.json();
	return async (method, path, body, extra = {}) => {
		const res = await fetch(base + '/api/v1' + path, {
			method,
			headers: { cookie, 'x-csrf-token': csrf_token, ...(body !== undefined ? { 'content-type': typeof body === 'string' ? 'text/plain' : 'application/json' } : {}), ...extra },
			body: body === undefined ? undefined : typeof body === 'string' ? body : JSON.stringify(body)
		});
		return res.status === 204 ? null : res.headers.get('content-type')?.includes('json') ? res.json() : res.text();
	};
}
const admin = await apiSession('admin@e2e.test', adminPw);
await admin('POST', '/users', { email: 'viewer@e2e.test', password: userPw, role: 'user' });

// ---- browser ----
mkdirSync(OUT, { recursive: true });
const browser = await chromium.launch({ executablePath: CHROME });
const consoleErrors = [];
async function newPage(viewport = { width: 1440, height: 900 }, colorScheme = 'light') {
	const ctx = await browser.newContext({ viewport, baseURL: base, colorScheme });
	const page = await ctx.newPage();
	page.on('console', (m) => m.type() === 'error' && !/Failed to load resource/.test(m.text()) && consoleErrors.push(m.text()));
	page.on('pageerror', (e) => consoleErrors.push(String(e)));
	return page;
}
async function signIn(page, email, password) {
	await page.goto('/login');
	await page.getByLabel('Email').fill(email);
	await page.getByLabel('Password').fill(password);
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.waitForURL(base + '/dashboard');
}
async function noOverflow(page) {
	return page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);
}

const page = await newPage();
let botId = '';

await step('sign-in error keeps the email and announces the problem', async () => {
	await page.goto('/login');
	await page.getByLabel('Email').fill('admin@e2e.test');
	await page.getByLabel('Password').fill('not-the-password');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByRole('alert').waitFor();
	expect((await page.getByLabel('Email').inputValue()) === 'admin@e2e.test', 'email was cleared');
	expect((await page.getByLabel('Password').inputValue()) === '', 'password kept after failure');
});

await step('sign in and see the empty overview with ways to start', async () => {
	await page.getByLabel('Password').fill(adminPw);
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.waitForURL(base + '/dashboard');
	await page.getByRole('heading', { level: 1, name: 'Overview' }).waitFor();
	const bots = page.getByRole('region', { name: 'Discord bots' });
	await bots.getByText(/No bots .*yet/).waitFor();
	await bots.getByRole('link', { name: 'Start from a template' }).waitFor();
});

await step('the public home and panel dashboard have distinct routes', async () => {
	await page.goto('/');
	await page.getByRole('heading', { level: 1, name: /^Run every bot/ }).waitFor();
	expect((await page.getByRole('link', { name: 'Open panel', exact: true }).getAttribute('href')) === '/dashboard', 'Open panel does not target the dashboard');
	await page.goto('/dashboard');
});

await step('the desktop sidebar collapses to icons and remembers the choice', async () => {
	await page.getByRole('button', { name: 'Collapse sidebar' }).click();
	expect((await page.locator('aside[aria-label="Sidebar"]').innerText()).trim() === '', 'collapsed sidebar still shows labels');
	expect((await page.evaluate(() => localStorage.getItem('rivetpanel.sidebarCollapsed'))) === '1', 'collapsed state was not saved');
	await page.reload();
	await page.getByRole('button', { name: 'Expand sidebar' }).waitFor();
	await page.getByRole('button', { name: 'Expand sidebar' }).click();
});

await step('appearance accents use tuned light and dark colors', async () => {
	await page.goto('/settings/appearance');
	await page.getByRole('button', { name: /Ocean/ }).click();
	await page.getByRole('button', { name: /Dark/ }).click();
	expect((await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--color-action').trim())) === '#6f9cff', 'dark Ocean accent is wrong');
	await page.getByRole('button', { name: /Light/ }).click();
	expect((await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--color-action').trim())) === '#2458d3', 'light Ocean accent is wrong');
	await page.goto('/dashboard');
});

await step('the square corner style removes rounding everywhere and is remembered', async () => {
	await page.goto('/settings/appearance');
	await page.getByRole('button', { name: /^Square/ }).click();
	const radius = () => page.evaluate(() => getComputedStyle(document.querySelector('.btn')).borderTopLeftRadius);
	expect((await page.getAttribute('html', 'data-radius')) === 'none', 'data-radius is not none');
	expect((await radius()) === '0px', `button radius ${await radius()}`);
	await page.reload();
	expect((await page.getAttribute('html', 'data-radius')) === 'none', 'the corner style was not remembered');
	await page.getByRole('button', { name: /^Rounded/ }).click();
	expect((await radius()) === '9px', `rounded button radius ${await radius()}`);
	await page.getByRole('button', { name: /^Default/ }).click();
	expect((await radius()) === '6px', `default button radius ${await radius()}`);
	await page.goto('/dashboard');
});

await step('guided creation validates required values and creates the bot', async () => {
	await page.getByRole('link', { name: 'Start from a template' }).click();
	await page.getByRole('radio', { name: /discord\.js starter/ }).click();
	await page.getByRole('button', { name: 'Continue' }).click();
	await page.getByRole('button', { name: 'Continue' }).click(); // no token yet
	expect((await page.locator('input[aria-invalid="true"]').count()) === 1, 'missing token not flagged');
	await page.locator('input[type="password"]').fill('placeholder-token-for-tests');
	await page.getByRole('button', { name: 'Continue' }).click();
	await page.getByRole('heading', { name: 'Review and create' }).waitFor();
	await page.getByRole('button', { name: /Create/ }).click();
	await page.waitForURL(/\/bots\/[0-9a-f-]{36}\?tab=manage/);
	botId = page.url().match(/bots\/([0-9a-f-]{36})/)[1];
	await page.getByRole('heading', { name: 'Setup' }).waitFor();
});

await step('overview explains the state without a runner', async () => {
	await page.getByText(/No runner|Stopped/).first().waitFor();
});

await step('grouped section navigation keeps deep links and history', async () => {
	await page.getByRole('link', { name: 'Env', exact: true }).click();
	await page.waitForURL(/tab=env/);
	await page.getByText('DISCORD_TOKEN').first().waitFor();
	await page.getByRole('link', { name: 'Files' }).click();
	await page.waitForURL(/tab=files/);
	await page.goBack();
	await page.waitForURL(/tab=env/);
});

await step('unsaved edits are protected when leaving the section', async () => {
	await page.goto(`/bots/${botId}?tab=files`);
	await page.getByRole('button', { name: 'index.js', exact: true }).click();
	await page.locator('.cm-content').waitFor();
	await page.locator('.cm-content').click();
	await page.keyboard.type('// edited\n');
	await page.getByText('unsaved changes').waitFor();
	await page.getByRole('link', { name: 'Startup' }).click();
	await page.getByRole('heading', { name: 'Unsaved changes' }).waitFor();
	await page.getByRole('button', { name: 'Keep editing' }).click();
	expect(page.url().includes('tab=files'), 'navigated away despite Keep editing');
	await page.getByRole('link', { name: 'Startup' }).click();
	await page.getByRole('button', { name: 'Discard changes' }).click();
	await page.waitForURL(/tab=startup/);
});

await step('a save after someone else changed the file shows a conflict, not an overwrite', async () => {
	await page.goto(`/bots/${botId}?tab=files`);
	await page.getByRole('button', { name: 'README.md', exact: true }).click();
	await page.locator('.cm-content').click();
	await page.keyboard.type('mine ');
	await admin('PUT', `/bots/${botId}/files/content?path=README.md`, 'theirs');
	await page.keyboard.press('Control+s');
	await page.getByRole('heading', { name: 'This file changed' }).waitFor();
	await page.getByRole('button', { name: 'Keep editing' }).click();
	const onDisk = await admin('GET', `/bots/${botId}/files/content?path=README.md`);
	expect(onDisk === 'theirs', 'the stale save overwrote the file');
	await page.getByText('unsaved changes').waitFor(); // the draft is still there
});

await step('restore review: Cancel changes nothing', async () => {
	await admin('POST', `/bots/${botId}/backups`, { include_env: true });
	await new Promise((r) => setTimeout(r, 800));
	// The previous step left an unsaved edit: a full page load must ask first
	// (the browser's own leave-page prompt), and continues when accepted.
	let prompted = false;
	page.once('dialog', (d) => {
		prompted = d.type() === 'beforeunload';
		d.accept();
	});
	await page.goto(`/bots/${botId}?tab=backups`);
	expect(prompted, 'leaving with unsaved edits did not ask');
	await page.getByRole('button', { name: 'Restore…' }).first().click();
	await page.getByRole('heading', { name: 'Restore this backup?' }).waitFor();
	await page.getByRole('button', { name: 'Cancel' }).click();
	const ops = await admin('GET', `/bots/${botId}/operations?kind=restore`);
	expect(ops.operations.length === 0, 'a cancelled restore ran');
});

await step('menus open from the keyboard and Escape returns focus', async () => {
	await page.goto('/dashboard');
	const trigger = page.getByRole('button', { name: /Actions for/ }).first();
	await trigger.focus();
	await page.keyboard.press('Enter');
	await page.getByRole('menu').waitFor();
	await page.keyboard.press('Escape');
	expect(await trigger.evaluate((el) => el === document.activeElement), 'focus did not return to the menu button');
});

await step('a shared console-only user sees output but no power or input controls', async () => {
	await admin('PUT', `/bots/${botId}/users`, { email: 'viewer@e2e.test', permissions: 1 });
	const p = await newPage();
	await signIn(p, 'viewer@e2e.test', userPw);
	await p.goto(`/bots/${botId}?tab=console`);
	await p.getByRole('link', { name: 'Manage', exact: true }).waitFor(); // viewing is allowed
	await p.getByText(/Live output needs the Docker runner|Bot output/).first().waitFor();
	expect((await p.getByRole('group', { name: 'Power controls' }).count()) === 0, 'power controls shown');
	expect((await p.getByLabel('Send a line to the bot').count()) === 0, 'stdin input shown');
	expect((await p.getByRole('link', { name: 'Files' }).count()) === 0, 'Files section shown');
	await p.context().close();
});

await step('Ctrl+K jumps to a bot by name', async () => {
	await page.goto('/activity');
	await page.getByRole('heading', { name: 'Activity' }).waitFor();
	await page.keyboard.press('Control+k');
	await page.getByPlaceholder(/Search bots, pages/).fill('discord');
	await page.keyboard.press('Enter');
	await page.waitForURL(new RegExp(`/bots/${botId}`));
});

await step('Ask AI opens one chat that knows which bot and section is in view', async () => {
	await page.goto(`/bots/${botId}?tab=files`);
	await page.getByRole('link', { name: 'Files', exact: true }).waitFor();
	expect((await page.getByRole('link', { name: 'AI operator' }).count()) === 0, 'the per-bot AI tab is still there');
	await page.getByRole('button', { name: /^Ask AI\b/ }).click();
	const chat = page.getByRole('dialog', { name: 'AI assistant' });
	await chat.waitFor();
	// No provider is configured in this panel: the chat says so instead of failing.
	await chat.getByText('The assistant is not set up yet').waitFor();
	expect((await chat.getByTitle(/This page is shared with the assistant/).textContent()).toLowerCase().includes('files'), 'the context chip does not name the section');
	await chat.getByRole('button', { name: 'Do not share this page with the assistant' }).click();
	await chat.getByRole('button', { name: /^Share/ }).waitFor();
	// It follows the person to another page.
	await chat.getByRole('button', { name: 'Close' }).click();
	await page.getByRole('link', { name: 'Activity', exact: true }).first().click();
	await page.getByRole('button', { name: /^Ask AI\b/ }).click();
	await page.getByRole('dialog', { name: 'AI assistant' }).getByTitle(/This page is shared with the assistant: \/activity/).waitFor();
	await page.keyboard.press('Escape');
});

await step('the Changes view lists who changed what, without values', async () => {
	await page.goto('/activity?view=changes');
	await page.getByText('created the bot').first().waitFor();
	await page.getByText('revealed a variable').first().waitFor().catch(() => {});
	expect(!(await page.content()).includes('placeholder-token-for-tests'), 'a secret value is on the page');
});

await step('the workspace switcher scopes the bot list', async () => {
	const ws = await admin('POST', '/workspaces', { name: 'E2E team' });
	await page.goto('/bots');
	await page.getByRole('button', { name: 'Switch workspace' }).first().click();
	await page.getByRole('menuitem', { name: /E2E team/ }).click();
	await page.getByRole('region', { name: 'Bot list' }).getByText(/No bots in E2E team yet/).waitFor();
	expect((await page.evaluate(() => localStorage.getItem('rivetpanel.workspace'))) === ws.workspace.id, 'the selection was not remembered');
	await page.getByRole('button', { name: 'Show all workspaces' }).click();
});

await step('tags filter the fleet and favorites come first', async () => {
	await admin('POST', '/bots', { name: 'zz second', runtime: 'python' });
	await admin('PUT', `/bots/${botId}/tags`, { tags: ['music'] });
	await page.goto('/bots');
	await page.getByRole('button', { name: 'Show items tagged music' }).click();
	await page.waitForURL(/tag=music/);
	expect((await page.getByRole('link', { name: 'zz second' }).count()) === 0, 'tag filter did not filter');
	await page.goto('/bots?sort=name');
	await page.getByRole('button', { name: 'Actions for zz second' }).click();
	await page.getByRole('menuitem', { name: 'Add to favorites' }).click();
	await page.reload();
	const list = page.getByRole('region', { name: 'Bot list' });
	await list.getByRole('link', { name: 'zz second' }).waitFor();
	const first = await list.getByRole('link').first().textContent();
	expect(first?.includes('zz second'), 'the favorite is not listed first');
});

// Visual review at three sizes and page-wide overflow checks.
const pages = ['/', '/register', '/dashboard', '/bots', '/bots/new', `/bots/${botId}?tab=manage`, `/bots/${botId}?tab=files`, `/bots/${botId}?tab=env`, `/bots/${botId}?tab=usage`, `/bots/${botId}?tab=backups`, `/bots/${botId}?tab=deploy`, `/bots/${botId}?tab=schedules`, `/bots/${botId}?tab=alerts`, `/bots/${botId}?tab=users`, `/bots/${botId}?tab=settings`, '/activity', '/sites', '/settings/profile', '/settings/appearance', '/settings/workspaces', '/settings/connected-accounts', '/settings/security', '/settings/sftp', '/admin/users', '/admin/workspaces', '/admin/sites', '/admin/settings', '/admin/modules', '/admin/host', '/admin/host?tab=bots', '/admin/host?tab=logs', '/admin/logs', '/admin/environment', '/admin/diagnostics'];
for (const [label, vp] of [['phone', { width: 375, height: 812 }], ['tablet', { width: 768, height: 1024 }], ['desktop', { width: 1440, height: 900 }]]) {
	const p = await newPage(vp);
	await signIn(p, 'admin@e2e.test', adminPw);
	for (const path of pages) {
		await step(`${label} ${path.replace(botId, ':bot')} has no page-wide horizontal scroll`, async () => {
			await p.goto(path);
			await p.waitForLoadState('networkidle').catch(() => {});
			await p.waitForTimeout(300);
			await p.screenshot({ path: join(OUT, `${label}-${path.replace(botId, 'bot').replace(/[^a-z0-9]+/gi, '_') || 'root'}.png`), fullPage: true });
			expect(await noOverflow(p), 'the page scrolls sideways');
		});
	}
	await p.context().close();
}

// Dark theme: follows the system, can be pinned, and survives a reload.
{
	const p = await newPage({ width: 1440, height: 900 }, 'dark');
	await signIn(p, 'admin@e2e.test', adminPw);
	await step('dark theme follows the system preference', async () => {
		await p.goto('/dashboard');
		expect((await p.getAttribute('html', 'data-theme')) === 'dark', 'data-theme is not dark');
		const bg = await p.evaluate(() => getComputedStyle(document.body).backgroundColor);
		expect(/rgb\(1[0-9], (8|9|1[0-9]), [0-9]+\)|rgba?\(11, 9, 8/.test(bg) || bg.includes('11, 9, 8'), `body background ${bg}`);
	});
	await step('the theme button pins light and dark', async () => {
		await p.getByRole('button', { name: /Switch theme/ }).click(); // system -> light
		expect((await p.getAttribute('html', 'data-theme')) === 'light', 'did not switch to light');
		await p.reload();
		expect((await p.getAttribute('html', 'data-theme')) === 'light', 'light was not remembered');
		await p.getByRole('button', { name: /Switch theme/ }).click(); // light -> dark
		expect((await p.getAttribute('html', 'data-theme')) === 'dark', 'did not switch to dark');
	});
	for (const path of ['/', '/register', '/dashboard', `/bots/${botId}?tab=manage`, `/bots/${botId}?tab=usage`, `/bots/${botId}?tab=schedules`, `/bots/${botId}?tab=alerts`, '/settings/profile', '/settings/appearance', '/settings/security', '/admin/settings', '/login']) {
		await step(`dark ${path.replace(botId, ':bot')} renders`, async () => {
			await p.goto(path);
			await p.waitForLoadState('networkidle').catch(() => {});
			await p.waitForTimeout(300);
			await p.screenshot({ path: join(OUT, `dark-${path.replace(botId, 'bot').replace(/[^a-z0-9]+/gi, '_') || 'root'}.png`), fullPage: true });
			expect(await noOverflow(p), 'the page scrolls sideways');
		});
	}
	await p.context().close();
}

await step('AI research settings save (enable, key) and survive a reload', async () => {
	const p = await newPage();
	await signIn(p, 'admin@e2e.test', adminPw);
	await p.goto('/admin/settings');
	const research = p.locator('section', { has: p.getByRole('heading', { name: 'Web research' }) });
	await research.waitFor();
	await research.getByLabel('Search the web').check();
	await research.getByLabel('API keys').fill('e2e-search-key');
	expect(await research.getByRole('button', { name: 'Test search' }).isDisabled(), 'Test search runs against unsaved settings');
	await research.getByRole('button', { name: 'Save', exact: true }).click();
	await p.getByText('Research settings saved').waitFor();
	expect((await research.getByRole('alert').count()) === 0, 'save showed an error');
	await p.reload();
	await research.waitFor();
	expect(await research.getByLabel('Search the web').isChecked(), 'search is not enabled after a reload');
	expect((await research.getByLabel('API keys').getAttribute('placeholder'))?.startsWith('1 encrypted key'), 'the key was not stored');
	expect(!(await research.getByRole('button', { name: 'Test search' }).isDisabled()), 'Test search stays disabled after saving');
	await p.context().close();
});

await step('log settings show the per-day file limit', async () => {
	const p = await newPage();
	await signIn(p, 'admin@e2e.test', adminPw);
	await p.goto('/admin/logs');
	const field = p.getByLabel('Daily file limit');
	await field.waitFor();
	expect((await field.inputValue()) === '256', `daily file limit ${await field.inputValue()}`);
	await p.context().close();
});

await step('no unexpected browser console errors', async () => {
	expect(consoleErrors.length === 0, consoleErrors.slice(0, 3).join(' | '));
});

await browser.close();
panel.kill();
rmSync(dir, { recursive: true, force: true });
const failed = results.filter((r) => !r.ok);
console.log(`\n${results.length - failed.length} passed, ${failed.length} failed. Screenshots in ${OUT}`);
if (failed.length) {
	if (process.env.E2E_PANEL_LOG) console.log(panelLog.slice(-4000));
	process.exit(1);
}
