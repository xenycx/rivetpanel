<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import { joinArgs, splitArgs } from '$lib/args';
	import type { Bot, RuntimeInfo } from '$lib/api/types';
	import { onMount, untrack } from 'svelte';
	import { registerDirty } from '$lib/ui/guard.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	let { bot, stopped, onSaved }: { bot: Bot; stopped: boolean; onSaved: (b: Bot) => void } = $props();

	// The form is a draft of the saved settings: it starts from them and
	// follows later changes of the bot (a save, another tab) while it has no
	// unsaved edits.
	const fromBot = (b: Bot) => ({
		runtime: b.runtime,
		command: joinArgs(b.argv),
		entrypoint: joinArgs(b.entrypoint),
		buildCommand: b.build_command ?? '',
		policy: b.restart_policy,
		maxAttempts: b.restart_max_attempts,
		backoffInit: b.restart_backoff_initial_ms / 1000,
		backoffMax: b.restart_backoff_max_ms / 1000
	});
	const saved = $derived(fromBot(bot));
	const start = (() => fromBot(bot))();

	let runtimes = $state<RuntimeInfo[]>([]);
	let runtime = $state(start.runtime);
	let command = $state(start.command);
	let entrypoint = $state(start.entrypoint);
	let buildCommand = $state(start.buildCommand);
	let policy = $state(start.policy);
	let maxAttempts = $state(start.maxAttempts);
	let backoffInit = $state(start.backoffInit);
	let backoffMax = $state(start.backoffMax);
	let error = $state('');
	const snapshot = () => JSON.stringify([runtime, command, entrypoint, buildCommand, policy, maxAttempts, backoffInit, backoffMax]);
	let base = $state(snapshot());
	const dirty = $derived(snapshot() !== base);
	let syncedFrom = JSON.stringify(start);
	$effect.pre(() => {
		const s = saved;
		const key = JSON.stringify(s);
		untrack(() => {
			if (key === syncedFrom) return;
			syncedFrom = key;
			if (dirty) return; // keep unsaved edits
			({ runtime, command, entrypoint, buildCommand, policy, maxAttempts, backoffInit, backoffMax } = s);
			base = snapshot();
		});
	});

	onMount(() => {
		api<{ runtimes: RuntimeInfo[] }>('GET', '/runtimes').then((r) => (runtimes = r.runtimes));
		return registerDirty({ label: 'Startup settings', isDirty: () => dirty, save: async () => await save() });
	});
	const chosen = $derived(runtimes.find((r) => r.id === runtime));
	const changedRuntime = $derived(runtime !== bot.runtime);

	function useDefault() {
		if (chosen) {
			command = joinArgs(chosen.default_argv);
			entrypoint = '';
		}
	}

	async function save(e?: SubmitEvent): Promise<boolean> {
		e?.preventDefault();
		error = '';
		const body: Record<string, unknown> = {
			entrypoint: splitArgs(entrypoint),
			build_command: buildCommand,
			restart_policy: policy,
			restart_max_attempts: maxAttempts,
			restart_backoff_initial_ms: Math.round(backoffInit * 1000),
			restart_backoff_max_ms: Math.round(backoffMax * 1000)
		};
		const argv = splitArgs(command);
		// Switching runtime without typing a command adopts the new runtime's default.
		if (!(changedRuntime && command === joinArgs(bot.argv))) body.argv = argv;
		if (changedRuntime) body.runtime = runtime;
		try {
			onSaved(await api<Bot>('PATCH', `/bots/${bot.id}`, body));
			base = snapshot();
			toast('Startup settings saved');
			return true;
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The startup settings could not be saved.';
			return false;
		}
	}
</script>

<form onsubmit={save}>
	<SettingsSection title="Command" description="What the container runs when the bot starts. Changes need a stopped bot and apply at the next start.">
		<div class="grid gap-5">
			<label class="block">
				<span class="label">Runtime</span>
				<select class="field" bind:value={runtime} disabled={!stopped}>
					{#each runtimes as r (r.id)}<option value={r.id}>{r.display_name}</option>{/each}
				</select>
				{#if changedRuntime}<span class="help text-warn!">Changing the runtime switches the image and resets the start command to its default unless you edit it.</span>{/if}
			</label>

			<label class="block">
				<span class="label">Start command</span>
				<input class="field font-mono text-[13px]" bind:value={command} disabled={!stopped} spellcheck="false" placeholder="node index.js" />
				<span class="help">Program and arguments, for example <code class="font-mono">node index.js</code>, <code class="font-mono">python bot.py</code> or <code class="font-mono">./app</code>. Quote arguments containing spaces. It runs directly, not through a shell, and only programs approved for the runtime are accepted.</span>
				{#if chosen}<button type="button" class="btn btn-quiet mt-1 px-0 text-action" onclick={useDefault} disabled={!stopped}>Use the default: {joinArgs(chosen.default_argv)}</button>{/if}
			</label>

			<label class="block">
				<span class="label">Custom entrypoint <span class="font-normal text-muted">(optional)</span></span>
				<input class="field font-mono text-[13px]" bind:value={entrypoint} disabled={!stopped} spellcheck="false" placeholder="node --enable-source-maps" />
				<span class="help">Overrides the container entrypoint; the start command above then becomes its arguments. Leave empty to run the command directly.</span>
			</label>
		</div>
	</SettingsSection>

	<SettingsSection title="Build" description="Runs before every start in a separate, resource-limited container with internet access but without the bot's variables. Files it writes into the workspace stay for the bot.">
		<label class="block">
			<span class="label">Custom build command <span class="font-normal text-muted">(optional)</span></span>
			<textarea class="field min-h-28 font-mono text-[13px]" bind:value={buildCommand} disabled={!stopped} spellcheck="false" maxlength="4096"
				placeholder={chosen?.has_build ? 'Leave empty to use the default build for ' + chosen.display_name : 'cd cmd/mybot && go build -o /workspace/app .'}></textarea>
			<span class="help">Shell commands, one per line, run with <code class="font-mono">sh</code> in <code class="font-mono">/workspace</code>; the build stops at the first failing line. Leave empty to use the runtime's default (for example <code class="font-mono">npm ci</code> or installing <code class="font-mono">requirements.txt</code>).</span>
		</label>
	</SettingsSection>

	<SettingsSection title="Auto-restart" description="What happens when the process exits. The delay doubles after every crash, up to the longest delay, and starts over after the bot runs stably for a minute.">
		<fieldset class="grid min-w-0 gap-4 sm:grid-cols-2 xl:grid-cols-4" disabled={!stopped}>
			<legend class="sr-only">Auto-restart</legend>
			<label class="block sm:col-span-2 xl:col-span-4">
				<span class="label">When the bot exits</span>
				<select class="field" bind:value={policy}>
					<option value="on_failure">Restart it after a crash (non-zero exit)</option>
					<option value="never">Never restart it automatically</option>
				</select>
				<span class="help">A clean exit (code 0) is never restarted. Pressing Start again always retries.</span>
			</label>
			<label class="block xl:col-span-2"><span class="label">Give up after (crashes in a row)</span><input class="field" type="number" step="1" min="0" max="100" bind:value={maxAttempts} /><span class="help">0 keeps trying forever.</span></label>
			<label class="block"><span class="label">First delay (s)</span><input class="field" type="number" min="0.1" step="any" bind:value={backoffInit} /></label>
			<label class="block"><span class="label">Longest delay (s)</span><input class="field" type="number" min="0.1" step="any" bind:value={backoffMax} /></label>
		</fieldset>
	</SettingsSection>

	{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	<div class="sticky bottom-0 z-10 flex items-center gap-3 border-t border-rule-soft bg-paper/95 py-3 backdrop-blur-sm">
		<button class="btn btn-primary" disabled={!stopped || !dirty}>Save startup settings</button>
		{#if dirty}<span class="text-small text-warn">Unsaved changes</span>{/if}
	</div>
</form>
