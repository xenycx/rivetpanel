<script lang="ts">
	// A byte count typed as a number and a unit. The value stays a plain integer
	// string of bytes (what the environment variable holds); blank means unset.
	let { value, onchange, id, placeholder = '', disabled = false, invalid = false }: { value: string; onchange: (v: string) => void; id?: string; placeholder?: string; disabled?: boolean; invalid?: boolean } = $props();

	const units = [
		{ label: 'KiB', mult: 1024 },
		{ label: 'MiB', mult: 1024 ** 2 },
		{ label: 'GiB', mult: 1024 ** 3 },
		{ label: 'TiB', mult: 1024 ** 4 }
	];
	function fit(v: string): number {
		const n = Number(v);
		if (!v || !Number.isFinite(n) || n <= 0) return 1; // MiB
		let best = 0;
		units.forEach((u, i) => {
			if (n >= u.mult && n % u.mult === 0) best = i;
		});
		return best;
	}
	let unit = $state(1); // MiB
	let text = $state('');
	let lastEmitted: string | null = null;
	const show = (v: string, u: number) => (v === '' || !Number.isFinite(Number(v)) ? '' : String(+(Number(v) / units[u].mult).toFixed(4)));
	// Reads the value at first and whenever it changed from outside this field
	// (a reset, a discard); an edit made here is remembered so it is not re-read.
	$effect(() => {
		if (value !== lastEmitted) {
			unit = fit(value);
			text = show(value, unit);
			lastEmitted = value;
		}
	});
	function emit() {
		const n = Number(text);
		const v = text.trim() === '' ? '' : Number.isFinite(n) && n >= 0 ? String(Math.round(n * units[unit].mult)) : value;
		lastEmitted = v;
		onchange(v);
	}
</script>

<div class="flex gap-2">
	<input {id} class="field min-w-0 flex-1 font-mono" type="number" min="0" step="any" {placeholder} {disabled} aria-invalid={invalid ? 'true' : undefined} bind:value={text} oninput={emit} />
	<select class="field w-24 shrink-0" aria-label="Unit" {disabled} bind:value={unit} onchange={emit}>
		{#each units as u, i (u.label)}<option value={i}>{u.label}</option>{/each}
	</select>
</div>
