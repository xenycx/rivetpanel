import { api } from '$lib/api/client';

/**
 * Asks the panel to restart (built-in administrators; the service manager
 * starts it again) and waits up to 90 seconds for it to go down and come back.
 * Resolves true once it answers again after going down.
 */
export async function restartPanel(): Promise<boolean> {
	await api('POST', '/admin/environment/restart');
	let sawDown = false;
	for (let i = 0; i < 90; i++) {
		await new Promise((r) => setTimeout(r, 1000));
		try {
			const r = await fetch('/api/v1/healthz', { cache: 'no-store' });
			if (r.ok && sawDown) return true;
			if (!r.ok) sawDown = true;
		} catch {
			sawDown = true;
		}
	}
	return false;
}

/** The confirmation shown before a restart. */
export const restartConfirm = {
	title: 'Restart RivetPanel now?',
	body: 'The panel exits and your service manager (systemd, or Docker’s restart policy) starts it again with the saved values. The page is unavailable for a few seconds. Apps keep running.\nIf nothing starts it again, the panel stays down until you start it yourself.',
	confirmLabel: 'Restart panel',
	tone: 'danger' as const
};
