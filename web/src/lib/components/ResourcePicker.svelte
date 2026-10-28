<script lang="ts">
	import { fmtBytes, fmtCpu } from '$lib/api/client';
	import BytesField from '$lib/components/BytesField.svelte';

	// Memory (bytes) or CPU (nano-CPUs, 1e9 = one core) chosen from preset chips,
	// with a Custom chip that reveals an exact field. `null` means "not set"
	// and is offered as the leading chip when allowEmpty is on.
	let {
		kind,
		value,
		onchange,
		min = 0,
		max = null,
		allowEmpty = false,
		emptyLabel = 'Default',
		disabled = false,
		id,
		invalid = false,
		labelledby
	}: {
		kind: 'memory' | 'cpu';
		value: number | null;
		onchange: (v: number | null) => void;
		min?: number;
		max?: number | null;
		allowEmpty?: boolean;
		emptyLabel?: string;
		disabled?: boolean;
		id?: string;
		invalid?: boolean;
		/** Id of the element that names the group; falls back to "Memory" / "CPU". */
		labelledby?: string;
	} = $props();

	const MiB = 1024 ** 2;
	const GiB = 1024 ** 3;
	const memoryPresets = [256 * MiB, 512 * MiB, GiB, 2 * GiB, 4 * GiB, 8 * GiB, 16 * GiB, 32 * GiB];
	const cpuPresets = [0.5, 1, 2, 4, 8].map((c) => c * 1e9);

	const presets = $derived((kind === 'memory' ? memoryPresets : cpuPresets).filter((p) => p >= min && (max == null || p <= max)));
	const label = (p: number) => (kind === 'memory' ? fmtBytes(p).replace('.0 ', ' ') : fmtCpu(p).replace(' CPU', p === 1e9 ? ' core' : ' cores'));

	// Custom stays open once chosen, and while the value matches no preset.
	let customOpen = $state(false);
	const matched = $derived(value != null && presets.includes(value));
	const showCustom = $derived(customOpen || (value != null && !matched));
	const emptyOn = $derived(allowEmpty && value == null && !showCustom);

	// The CPU field edits cores as text; the memory field is BytesField.
	let cpuText = $state('');
	let lastEmitted: number | null | undefined;
	const cores = (v: number | null) => (v == null ? '' : String(+(v / 1e9).toFixed(4)));
	// Re-reads the value when it changed from outside (a save, a reset); an edit
	// made here is remembered so typing is not rewritten under the cursor.
	$effect(() => {
		if (value === lastEmitted) return;
		lastEmitted = value;
		cpuText = cores(value);
		if (value == null || presets.includes(value)) customOpen = false;
	});

	function emit(v: number | null) {
		lastEmitted = v;
		onchange(v);
	}
	function pick(v: number | null) {
		customOpen = false;
		emit(v);
	}
	function openCustom() {
		customOpen = true;
		cpuText = cores(value);
	}
	function setBytes(s: string) {
		const n = Number(s);
		emit(s.trim() === '' || !Number.isFinite(n) ? null : Math.round(n));
	}
	function setCpu(e: Event) {
		cpuText = (e.currentTarget as HTMLInputElement).value;
		const n = Number(cpuText);
		emit(cpuText.trim() === '' || !Number.isFinite(n) || n < 0 ? null : Math.round(n * 1e9));
	}

	const chip = (on: boolean) => `rounded-control border px-2.5 py-1 font-mono text-small disabled:cursor-not-allowed disabled:opacity-60 ${on ? 'border-action bg-action/10 text-ink' : 'border-rule-soft bg-raised text-muted enabled:hover:border-rule enabled:hover:text-ink'}`;
	const outOfRange = $derived(value != null && (value < min || (max != null && value > max)));
	const bad = $derived(invalid || outOfRange);
	const name = $derived(kind === 'memory' ? 'Memory' : 'CPU');
	const customId = $derived(id ? `${id}-custom` : undefined);
</script>

<div {id} class="min-w-0" role="group" aria-label={labelledby ? undefined : name} aria-labelledby={labelledby}>
	<div class="flex flex-wrap gap-1.5">
		{#if allowEmpty}
			<button type="button" class={chip(emptyOn)} aria-pressed={emptyOn} {disabled} onclick={() => pick(null)}>{emptyLabel}</button>
		{/if}
		{#each presets as p (p)}
			{@const on = !showCustom && value === p}
			<button type="button" class={chip(on)} aria-pressed={on} {disabled} onclick={() => pick(p)}>{label(p)}</button>
		{/each}
		<button type="button" class={chip(showCustom)} aria-pressed={showCustom} {disabled} onclick={openCustom}>Custom</button>
	</div>
	{#if showCustom}
		<div class="mt-2 max-w-xs">
			{#if kind === 'memory'}
				<BytesField id={customId} value={value == null ? '' : String(value)} onchange={setBytes} {disabled} invalid={bad} />
			{:else}
				<div class="flex items-center gap-2">
					<input id={customId} class="field min-w-0 flex-1 font-mono" type="number" min={min / 1e9} max={max == null ? undefined : max / 1e9} step="0.25" aria-label="CPU cores" aria-invalid={bad ? 'true' : undefined} {disabled} value={cpuText} oninput={setCpu} />
					<span class="text-small text-muted">cores</span>
				</div>
			{/if}
		</div>
	{/if}
</div>
