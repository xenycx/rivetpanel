<script lang="ts">
	// Renders a Markdown subset as Svelte text nodes: raw HTML in the source is
	// shown literally, never interpreted. Links must be http(s) URLs; with
	// relativeLinks also same-site paths ("/support", never "//host").
	let { source = '', relativeLinks = false }: { source?: string; relativeLinks?: boolean } = $props();
	const tail = String.raw`|\*\*[^*\n]+?\*\*|__[^_\n]+?__|(?<![\w*])\*(?=\S)[^*\n]+?(?<=\S)\*(?![\w*])|(?<![\w_])_(?=\S)[^_\n]+?(?<=\S)_(?![\w_]))`;
	// \x60 is a backtick (code spans).
	const reAbs = new RegExp(String.raw`(\x60[^\x60\n]+\x60|\[[^\]\n]+\]\((https?:\/\/[^\s)]+)\)` + tail, 'gi');
	const reRel = new RegExp(String.raw`(\x60[^\x60\n]+\x60|\[[^\]\n]+\]\((https?:\/\/[^\s)]+|\/(?![\/\\])[^\s)"'<>\\\x60]*)\)` + tail, 'gi');
	type Inline = { kind: 'text' | 'code' | 'link' | 'strong' | 'em'; text: string; href?: string };
	type Block = { kind: 'p' | 'h' | 'code' | 'list' | 'quote' | 'table'; text?: string; lang?: string; items?: string[]; rows?: string[][] };
	function inline(text: string): Inline[] {
		const out: Inline[] = [];
		// Earliest match wins, so emphasis markers inside `code` stay literal.
		const re = relativeLinks ? reRel : reAbs;
		re.lastIndex = 0;
		let at = 0;
		for (const m of text.matchAll(re)) {
			if ((m.index ?? 0) > at) out.push({ kind: 'text', text: text.slice(at, m.index) });
			if (m[0].startsWith('`')) out.push({ kind: 'code', text: m[0].slice(1, -1) });
			else if (m[0].startsWith('**') || m[0].startsWith('__')) out.push({ kind: 'strong', text: m[0].slice(2, -2) });
			else if (m[0].startsWith('*') || m[0].startsWith('_')) out.push({ kind: 'em', text: m[0].slice(1, -1) });
			else out.push({ kind: 'link', text: m[0].slice(1, m[0].indexOf(']')), href: m[2] });
			at = (m.index ?? 0) + m[0].length;
		}
		if (at < text.length) out.push({ kind: 'text', text: text.slice(at) });
		return out;
	}
	// A table is a row with pipes followed by a |---|---| separator row.
	const tableStart = (lines: string[], i: number) => lines[i].includes('|') && i + 1 < lines.length && /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$/.test(lines[i + 1]);
	function parse(src: string): Block[] {
		const lines = src.replace(/\r/g, '').split('\n');
		const out: Block[] = [];
		for (let i = 0; i < lines.length; ) {
			const line = lines[i];
			if (!line.trim()) { i++; continue; }
			if (line.startsWith('```')) {
				const lang = line.slice(3).trim().slice(0, 30); const code: string[] = []; i++;
				while (i < lines.length && !lines[i].startsWith('```')) code.push(lines[i++]);
				i++; out.push({ kind: 'code', lang, text: code.join('\n') }); continue;
			}
			if (/^#{1,6}\s/.test(line)) { out.push({ kind: 'h', text: line.replace(/^#{1,6}\s+/, '') }); i++; continue; }
			if (/^>\s?/.test(line)) { const q: string[] = []; while (i < lines.length && /^>\s?/.test(lines[i])) q.push(lines[i++].replace(/^>\s?/, '')); out.push({ kind: 'quote', text: q.join(' ') }); continue; }
			if (/^\s*(?:[-*]|\d+\.)\s+/.test(line)) { const items: string[] = []; while (i < lines.length && /^\s*(?:[-*]|\d+\.)\s+/.test(lines[i])) items.push(lines[i++].replace(/^\s*(?:[-*]|\d+\.)\s+/, '')); out.push({ kind: 'list', items }); continue; }
			if (tableStart(lines, i)) { const raw = [line]; i += 2; while (i < lines.length && lines[i].includes('|')) raw.push(lines[i++]); const rows = raw.map((r) => r.replace(/^\||\|$/g, '').split('|').map((c) => c.trim())); out.push({ kind: 'table', rows: rows.slice(0, 40) }); continue; }
			const p: string[] = [line]; i++; while (i < lines.length && lines[i].trim() && !/^(?:```|#{1,6}\s|>\s?|\s*(?:[-*]|\d+\.)\s+)/.test(lines[i]) && !tableStart(lines, i)) p.push(lines[i++]); out.push({ kind: 'p', text: p.join(' ') });
		}
		return out;
	}
	const blocks = $derived(parse(source));
</script>

{#snippet spans(text: string)}
	{#each inline(text) as s}
		{#if s.kind === 'code'}<code>{s.text}</code>
		{:else if s.kind === 'strong'}<strong class="font-semibold">{@render spans(s.text)}</strong>
		{:else if s.kind === 'em'}<em>{@render spans(s.text)}</em>
		{:else if s.kind === 'link' && s.href?.startsWith('/')}<a class="link" href={s.href}>{s.text}</a>
		{:else if s.kind === 'link'}<a class="link" href={s.href} target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">{s.text}</a>
		{:else}{s.text}{/if}
	{/each}
{/snippet}

<div class="space-y-2 text-[0.9rem] leading-relaxed">
	{#each blocks as b}
		{#if b.kind === 'h'}<h3 class="pt-1 font-semibold text-ink">{@render spans(b.text ?? '')}</h3>
		{:else if b.kind === 'code'}<pre class="max-h-96 overflow-auto rounded-control border border-rule-soft bg-paper-2 p-3 font-mono text-small"><code data-language={b.lang}>{b.text}</code></pre>
		{:else if b.kind === 'list'}<ul class="list-disc space-y-1 pl-5">{#each b.items ?? [] as item}<li>{@render spans(item)}</li>{/each}</ul>
		{:else if b.kind === 'quote'}<blockquote class="border-l-2 border-action/50 pl-3 text-muted">{@render spans(b.text ?? '')}</blockquote>
		{:else if b.kind === 'table'}<div class="overflow-x-auto"><table class="w-full border-collapse text-left text-small">{#if b.rows?.length}<thead><tr class="border-b border-rule">{#each b.rows[0] as cell}<th class="px-2 py-1.5 font-semibold">{@render spans(cell)}</th>{/each}</tr></thead>{/if}<tbody>{#each (b.rows ?? []).slice(1) as row}<tr class="border-b border-rule-soft">{#each row as cell}<td class="px-2 py-1.5">{@render spans(cell)}</td>{/each}</tr>{/each}</tbody></table></div>
		{:else}<p>{@render spans(b.text ?? '')}</p>{/if}
	{/each}
</div>
