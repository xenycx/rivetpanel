import type { Bot, Phase } from '$lib/api/types';

export type Tone = 'run' | 'warn' | 'fail' | 'idle';

export type Described = {
	label: string;
	tone: Tone;
	/** Work in progress (drawn as a hatched spine; never a perpetual animation). */
	busy: boolean;
	/** One plain sentence explaining the state, empty when the label says it all. */
	detail: string;
	/** The most useful thing to do next, if anything. */
	next?: 'start' | 'retry' | 'build-output' | 'console';
};

const reasonText: Record<string, string> = {
	build_failed: 'The build step failed.',
	setup_failed: 'The container could not be prepared.',
	crash_backoff: 'The bot crashed.',
	gave_up: 'The bot kept crashing, so automatic restarts stopped.',
	exited: 'The bot crashed and its restart policy is “never”.',
	runtime_missing: 'Its runtime was removed from this panel.',
	clean_exit: 'The process finished with exit code 0. Clean exits are not restarted.',
	killed: 'It was stopped with Kill.',
	cleanup_failed: 'Removing it did not finish. The panel keeps retrying.'
};

/** The reason sentence, naming a game server "the server" instead of "the bot". */
function reasonFor(b: Bot, reason: string): string | undefined {
	if (b.kind === 'game' && reason === 'build_failed') return 'The installation failed.';
	const t = reasonText[reason];
	return t && b.kind === 'game' ? t.replace(/^The bot\b/, 'The server') : t;
}

function exitPart(b: Bot): string {
	return b.last_exit_code !== null && b.last_exit_code !== 0 ? ` Exit code ${b.last_exit_code}.` : '';
}

/** Seconds (rounded up) until the next automatic retry, or null. */
export function retryIn(b: Bot, now = Date.now()): number | null {
	return b.next_retry_at_ms ? Math.max(0, Math.ceil((b.next_retry_at_ms - now) / 1000)) : null;
}

export function fmtCountdown(s: number): string {
	if (s < 60) return `${s} s`;
	const m = Math.floor(s / 60);
	return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
}

/** The single lifecycle vocabulary used by the fleet, bot header and overview. */
export function describe(b: Bot, now = Date.now()): Described {
	const phase: Phase = b.phase ?? 'checking';
	const reason = b.state_reason ?? '';
	switch (phase) {
		case 'running':
			return { label: 'Running', tone: 'run', busy: false, detail: '', next: 'console' };
		case 'queued':
			return { label: 'Starting', tone: 'warn', busy: true, detail: 'Start requested. Waiting for the runner to pick it up.' };
		case 'building':
			return { label: 'Building', tone: 'warn', busy: true, detail: 'Preparing the image and installing dependencies in a separate build container.' };
		case 'installing':
			return { label: 'Installing', tone: 'warn', busy: true, detail: 'Downloading and installing the server software. The first start of a new version takes a little longer.', next: 'build-output' };
		case 'starting':
			return { label: 'Starting', tone: 'warn', busy: true, detail: 'The container is starting.' };
		case 'restarting':
			return { label: 'Restarting', tone: 'warn', busy: true, detail: 'Replacing the container with the latest settings.' };
		case 'stopping':
			return { label: 'Stopping', tone: 'warn', busy: true, detail: 'Asking the process to exit.' };
		case 'retrying': {
			const s = retryIn(b, now);
			const when = s === null ? 'Retrying shortly.' : s === 0 ? 'Retrying now.' : `Next attempt in ${fmtCountdown(s)}.`;
			const what = reasonFor(b, reason) ?? 'The last attempt failed.';
			const count = reason === 'crash_backoff' && b.restart_count > 0 ? ` Crash ${b.restart_count} in a row.` : '';
			return {
				label: 'Waiting to retry',
				tone: 'warn',
				busy: true,
				detail: `${what}${exitPart(b)}${count} ${when}`,
				next: reason === 'build_failed' ? 'build-output' : 'console'
			};
		}
		case 'failed':
			if (reason === 'port_conflict')
				return { label: 'Port in use', tone: 'fail', busy: false, detail: b.last_error ?? 'A published port is already in use on this host.', next: 'retry' };
			return {
				label: 'Failed',
				tone: 'fail',
				busy: false,
				detail: `${reasonFor(b, reason) ?? 'The last run failed.'}${exitPart(b)} Fix the cause, then start it again.`,
				next: 'retry'
			};
		case 'exited':
			return { label: 'Exited', tone: 'idle', busy: false, detail: reasonText.clean_exit, next: 'start' };
		case 'checking':
			return { label: 'Checking host', tone: 'warn', busy: true, detail: 'Confirming the container state with Docker.' };
		case 'runner_offline':
			return { label: 'Runner offline', tone: 'fail', busy: false, detail: `Docker is not reachable. The ${b.kind === 'game' ? 'server' : 'bot'} starts as soon as the runner is back.` };
		case 'no_runner':
			return { label: 'No runner', tone: 'fail', busy: false, detail: `This panel runs without Docker, so ${b.kind === 'game' ? 'servers' : 'bots'} cannot be started here.` };
		case 'deleting':
			return { label: 'Deleting', tone: 'warn', busy: true, detail: '' };
		case 'stopped':
		default: {
			if (reason === 'killed') return { label: 'Stopped', tone: 'idle', busy: false, detail: reasonText.killed, next: 'start' };
			if (b.observed_state === 'failed' && b.last_error)
				return { label: 'Stopped', tone: 'idle', busy: false, detail: `The last run failed: ${b.last_error}`, next: 'start' };
			return { label: 'Stopped', tone: 'idle', busy: false, detail: '', next: 'start' };
		}
	}
}

/** True when configuration can change (the server enforces the same rule). */
export function isStopped(b: Bot): boolean {
	return b.desired_state === 'stopped' && ['stopped', 'failed', 'unknown'].includes(b.observed_state);
}

/** A container may be alive, so Kill makes sense. */
export function mayBeLive(b: Bot): boolean {
	return b.observed_state !== 'stopped' || b.desired_state === 'running';
}
