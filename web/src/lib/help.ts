import type { Bot } from '$lib/api/types';
import { fmtBytes } from '$lib/api/client';

export type Help = { title: string; body: string; action?: { label: string; href: string } };

/**
 * Turns a known failure into an explanation and a next step. The runner's
 * error text is generic and secret-free, so matching it is safe; unknown
 * errors return null and the raw message is shown on its own.
 */
export function helpFor(b: Bot, opts: { siteAdmin: boolean; buildMemory?: number }): Help | null {
	const e = (b.last_error ?? '').toLowerCase();
	const settings = { label: 'Open settings', href: `/bots/${b.id}?tab=settings` };
	if (e.includes('workspace ownership could not be prepared'))
		return {
			title: 'The panel cannot hand the files to the bot',
			body: opts.siteAdmin
				? 'The container runs as an unprivileged user that must own the workspace. On a rootful Docker host the panel needs root (the provided systemd unit runs it so) or CAP_CHOWN. For local testing, set RIVET_CONTAINER_USER to the panel’s own uid:gid.'
				: 'This is a host configuration problem. Ask the panel administrator to check the workspace ownership settings.',
			action: opts.siteAdmin ? { label: 'Open diagnostics', href: '/admin/diagnostics' } : undefined
		};
	if (e.includes('image is unavailable'))
		return {
			title: 'Docker could not download the image',
			body: 'The host needs internet access to the container registry, or a configured mirror. The panel retries automatically.',
			action: opts.siteAdmin ? { label: 'Open diagnostics', href: '/admin/diagnostics' } : undefined
		};
	if (e.includes('signal: 9') || e.includes('sigkill') || (e.includes('build failed') && e.includes('exit code 137')))
		return {
			title: 'The build ran out of memory',
			body: `The compiler was killed because the build container hit its memory limit${opts.buildMemory ? ` of ${fmtBytes(opts.buildMemory)}` : ''}. The host needs that much free memory while building; the running bot needs far less.`
		};
	if (e.includes('build timed out'))
		return { title: 'The build took too long', body: 'The build step has a time limit (RIVET_BUILD_TIMEOUT, 10 minutes by default). Large dependency trees on a slow host can exceed it.' };
	if (e.startsWith('build failed'))
		return { title: 'The build step failed', body: 'The output above shows the end of the build log. Open the build under Recent activity for the complete output, fix the project files, and start again.' };
	if (e.includes('environment could not be decrypted'))
		return {
			title: 'The environment variables cannot be decrypted',
			body: opts.siteAdmin
				? 'The encryption key used for them is missing from RIVET_KEY_DIR. Restore the key file, then run “rivetpanel verify”.'
				: 'The panel is missing an encryption key. Ask the administrator.'
		};
	if (e.includes('killed: out of memory'))
		return { title: 'The bot used more memory than its limit', body: `It is limited to ${fmtBytes(b.memory_bytes)}. Raise the limit in Settings, or reduce what the bot keeps in memory.`, action: settings };
	if (e.includes('container failed to start') || e.includes('container could not be created'))
		return { title: 'Docker could not start the container', body: 'Check the start command under Startup. If it looks right, the host may be out of resources.', action: { label: 'Open startup', href: `/bots/${b.id}?tab=startup` } };
	if (e.includes('runtime is not available'))
		return { title: 'The runtime was removed', body: 'Choose another runtime under Startup, or ask the administrator to restore it.' };
	if (b.phase === 'failed' && b.state_reason === 'gave_up')
		return { title: 'It keeps crashing', body: 'Open the console to see the last output before each crash, fix the cause, then start it again. The restart budget is set under Startup.', action: { label: 'Open console', href: `/bots/${b.id}?tab=manage` } };
	if ((b.phase === 'retrying' || b.phase === 'failed') && /exited with code [1-9]/.test(e))
		return { title: 'The process crashed', body: 'The console shows what it printed before exiting. A wrong or missing token is the most common cause for a new Discord bot.', action: { label: 'Open console', href: `/bots/${b.id}?tab=manage` } };
	return null;
}
