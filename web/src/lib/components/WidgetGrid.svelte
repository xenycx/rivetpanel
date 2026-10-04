<script lang="ts">
	import type { DashboardWidget } from '$lib/api/types';
	import { api, ApiError } from '$lib/api/client';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	let { widgets, botId = '', canAdmin = false, onRemoved = () => {} }: { widgets: DashboardWidget[]; botId?: string; canAdmin?: boolean; onRemoved?: () => void } = $props();
	let active = $state('');
	const groups = $derived([...new Set(widgets.map((w) => w.group || 'Overview'))]);
	const visible = $derived(widgets.filter((w) => (w.group || 'Overview') === (active || groups[0])));
	const number = (v: unknown, fallback = 0) => typeof v === 'number' && Number.isFinite(v) ? v : fallback;
	const text = (v: unknown) => typeof v === 'string' ? v : '';
	function safeURL(v: unknown) { try { const u=new URL(text(v)); return u.protocol==='https:'||u.protocol==='http:'?u.href:''; } catch { return ''; } }
	function tone(v: unknown) { return ['good','warn','bad'].includes(text(v)) ? text(v) : 'neutral'; }
	const rows = (v: unknown) => Array.isArray(v) ? v.slice(0,100) : [];
	function points(v: unknown) {
		const p=rows(v).slice(-60); const peak=Math.max(1,...p.map((x:any)=>number(x?.value))); const n=Math.max(1,p.length-1);
		return p.map((x:any,i)=>`${(i/n)*100},${36-(number(x?.value)/peak)*34}`).join(' ');
	}
	const spanClass = (w: DashboardWidget) => w.span === 3 ? 'sm:col-span-2 xl:col-span-3' : w.span === 2 ? 'sm:col-span-2' : '';
	async function remove(w: DashboardWidget) {
		if (!(await confirmDialog({ title: `Remove ${w.title}?`, body: 'This unpublishes the widget. The bot can publish it again in a later telemetry push.', confirmLabel: 'Remove widget', tone: 'danger' }))) return;
		try { await api('DELETE', `/bots/${botId}/widgets/${encodeURIComponent(w.key)}`); toast('Widget removed'); onRemoved(); }
		catch (e) { toast(e instanceof ApiError ? e.message : 'The widget could not be removed.', 'fail'); }
	}
</script>

{#if widgets.length}
	<section class="mt-5" aria-labelledby="custom-dashboard-title">
		<div class="flex flex-wrap items-baseline justify-between gap-3"><h2 id="custom-dashboard-title" class="text-title font-semibold">Bot dashboard</h2><span class="text-small text-muted">Published by the bot · {widgets.length} widget{widgets.length === 1 ? '' : 's'}</span></div>
		{#if groups.length > 1}<div class="mt-3 flex gap-1 overflow-x-auto border-b border-rule-soft" role="tablist" aria-label="Dashboard sections">{#each groups as group}<button class="px-3 py-2 text-small font-medium {(active || groups[0]) === group ? 'border-b-2 border-action text-ink' : 'text-muted'}" role="tab" aria-selected={(active || groups[0]) === group} onclick={() => active=group}>{group}</button>{/each}</div>{/if}
		<div class="mt-3 grid auto-rows-min gap-3 sm:grid-cols-2 xl:grid-cols-3">
			{#each visible as w (w.key)}
				<article class="min-w-0 rounded-tile border border-rule-soft bg-panel p-4 {spanClass(w)}" style:min-height={w.min_height ? `${w.min_height}px` : undefined}>
					<div class="flex items-center justify-between gap-2"><h3 class="truncate text-small font-medium text-muted">{w.title}</h3><span class="flex items-center gap-2">{#if w.stale}<span class="rounded-pill bg-warn/15 px-2 py-0.5 text-[11px] text-warn" title="This widget has not been refreshed for five minutes">Stale</span>{/if}{#if canAdmin && botId}<button class="text-[11px] text-muted hover:text-fail" onclick={() => remove(w)}>Remove</button>{/if}</span></div>
					{#if w.kind === 'metric'}
						<p class="mt-1 text-3xl font-semibold tabular-nums">{number(w.data.value).toLocaleString()} <span class="text-small font-normal text-muted">{text(w.data.unit)}</span></p>
						{#if number(w.data.delta)!==0}<p class="mt-1 text-small {number(w.data.delta)>0?'text-ok':'text-fail'}">{number(w.data.delta)>0?'↑':'↓'} {Math.abs(number(w.data.delta)).toLocaleString()} {text(w.data.detail)}</p>{:else if text(w.data.detail)}<p class="mt-1 text-small text-muted">{text(w.data.detail)}</p>{/if}
					{:else if w.kind === 'status'}
						<p class="mt-2 flex items-center gap-2"><span class="size-2.5 rounded-pill" class:bg-ok={tone(w.data.state)==='good'} class:bg-warn={tone(w.data.state)==='warn'} class:bg-fail={tone(w.data.state)==='bad'} class:bg-muted={tone(w.data.state)==='neutral'}></span><span class="text-title font-medium">{text(w.data.text) || 'Unknown'}</span></p>
					{:else if w.kind === 'progress' || w.kind === 'gauge'}
						{@const value=number(w.data.value)}{@const max=Math.max(1,number(w.data.max,100))}
						<div class="mt-3 h-2.5 overflow-hidden rounded-pill bg-paper-2"><div class="h-full bg-data" style={`width:${Math.max(0,Math.min(100,value/max*100))}%`}></div></div><p class="mt-2 text-small tabular-nums text-muted">{text(w.data.label)} {value.toLocaleString()} / {max.toLocaleString()} {text(w.data.unit)}</p>
					{:else if w.kind === 'text'}
						<div class="mt-2 whitespace-pre-wrap break-words leading-relaxed">{text(w.data.text)}</div>
					{:else if w.kind === 'markdown'}
						<div class="mt-2 space-y-1 break-words leading-relaxed">{#each text(w.data.text).split('\n') as line}{#if line.startsWith('### ')}<h6 class="font-semibold">{line.slice(4)}</h6>{:else if line.startsWith('## ')}<h5 class="text-title font-semibold">{line.slice(3)}</h5>{:else if line.startsWith('# ')}<h4 class="text-xl font-semibold">{line.slice(2)}</h4>{:else if line.startsWith('- ')}<p class="pl-4 before:mr-2 before:content-['•']">{line.slice(2)}</p>{:else if line.startsWith('> ')}<blockquote class="border-l-2 border-rule pl-3 text-muted">{line.slice(2)}</blockquote>{:else if line}<p>{line}</p>{:else}<div class="h-2"></div>{/if}{/each}</div>
					{:else if w.kind === 'code' || w.kind === 'log'}
						<pre class="mt-2 max-h-72 overflow-auto bg-term p-3 font-mono text-[12px] text-term-ink">{text(w.data.code) || text(w.data.text)}</pre>
					{:else if w.kind === 'link'}
						{#if safeURL(w.data.url)}<a class="btn mt-3" href={safeURL(w.data.url)} target="_blank" rel="noopener">{text(w.data.label)||'Open link'}</a>{:else}<p class="mt-2 text-small text-fail">The bot supplied an invalid link.</p>{/if}
					{:else if w.kind === 'image'}
						{#if safeURL(w.data.url)}<img class="mt-3 max-h-80 w-full object-contain" src={safeURL(w.data.url)} alt={text(w.data.alt) || w.title} loading="lazy" referrerpolicy="no-referrer" />{:else}<p class="mt-2 text-small text-fail">The bot supplied an invalid image URL.</p>{/if}
					{:else if w.kind === 'chart'}
						{@const ps=rows(w.data.points).slice(-40)}{@const peak=Math.max(1,...ps.map((p:any)=>number(p?.value)))}
						<div class="mt-3 flex h-32 items-end gap-1" aria-label={w.title}>{#each ps as p:any}<div class="min-w-1 flex-1 bg-data/70" style={`height:${Math.max(2,number(p?.value)/peak*100)}%`} title={`${text(p?.label)}: ${number(p?.value)} ${text(w.data.unit)}`}></div>{/each}</div>
					{:else if w.kind === 'line' || w.kind === 'area' || w.kind === 'sparkline'}
						<svg class="mt-3 h-36 w-full overflow-visible text-data" viewBox="0 0 100 38" preserveAspectRatio="none" role="img" aria-label={w.title}><polyline points={points(w.data.points)} fill="none" stroke="currentColor" stroke-width="1.5" vector-effect="non-scaling-stroke" /></svg>
					{:else if w.kind === 'donut'}
						{@const parts=rows(w.data.values)}{@const total=Math.max(1,parts.reduce((n:number,p:any)=>n+number(p?.value),0))}<div class="mt-3 flex flex-wrap items-center gap-4"><div class="grid size-28 place-items-center rounded-pill border-[18px] border-action/70"><strong>{total.toLocaleString()}</strong></div><ul class="text-small">{#each parts.slice(0,8) as p:any}<li>{text(p?.label)} <strong>{number(p?.value).toLocaleString()}</strong></li>{/each}</ul></div>
					{:else if w.kind === 'kv'}
						<dl class="mt-2 divide-y divide-rule-soft">{#each rows(w.data.items).slice(0,30) as item:any}<div class="flex justify-between gap-4 py-1.5"><dt class="text-muted">{text(item?.key)}</dt><dd class="break-all text-right font-medium">{text(item?.value) || number(item?.value).toLocaleString()}</dd></div>{/each}</dl>
					{:else if w.kind === 'heatmap'}
						{@const cells=rows(w.data.cells).slice(0,120)}{@const peak=Math.max(1,...cells.map((c:any)=>number(c?.value)))}<div class="mt-3 grid grid-cols-12 gap-1">{#each cells as c:any}<span class="aspect-square rounded-sm bg-action" style:opacity={Math.max(.12,number(c?.value)/peak)} title={`${text(c?.label)}: ${number(c?.value)}`}></span>{/each}</div>
					{:else if w.kind === 'table'}
						<div class="mt-2 max-h-80 overflow-auto"><table class="w-full text-left text-small"><thead class="sticky top-0 bg-panel"><tr>{#each rows(w.data.columns).slice(0,12) as c}<th class="border-b border-rule px-2 py-1 font-medium">{typeof c==='object'&&c?text((c as any).label):text(c)}</th>{/each}</tr></thead><tbody>{#each rows(w.data.rows).slice(0,50) as row:any}<tr>{#each rows(row).slice(0,12) as cell}<td class="border-b border-rule-soft px-2 py-1.5">{#if typeof cell==='object'&&cell&&safeURL((cell as any).url)}<a class="link" href={safeURL((cell as any).url)} target="_blank" rel="noopener">{text((cell as any).label)}</a>{:else}{typeof cell==='number'?cell.toLocaleString():text(cell)}{/if}</td>{/each}</tr>{/each}</tbody></table></div>
					{/if}
				</article>
			{/each}
		</div>
	</section>
{/if}
