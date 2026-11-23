// A group of charts that move together: one crosshair (uPlot cursor sync),
// one hover time, and one zoom. Dragging across any chart selects a time
// span for the whole group; double-clicking resets it.
//
// With `onrange` the selection is handed to the page (which usually turns it
// into an absolute time range and loads finer data). Without it the group
// zooms locally: every chart narrows its x axis to the selection.

let seq = 0;

export type Span = { start: number; end: number };

export class ChartGroup {
	/** uPlot cursor.sync key, unique per group. */
	readonly key = `rp-chart-group-${++seq}`;
	/** Time under the pointer in any chart of the group, or null. */
	hoverT = $state<number | null>(null);
	/** Local zoom (only used without onrange). */
	zoom = $state<Span | null>(null);
	onrange?: (s: Span) => void;
	onreset?: () => void;

	constructor(opts: { onrange?: (s: Span) => void; onreset?: () => void } = {}) {
		this.onrange = opts.onrange;
		this.onreset = opts.onreset;
	}

	/** A drag selection finished on one of the charts. */
	select(s: Span) {
		if (s.end - s.start < 1000) return; // a click, not a span
		if (this.onrange) this.onrange(s);
		else this.zoom = s;
	}

	/** Double-click: undo the zoom. */
	reset() {
		if (this.zoom) this.zoom = null;
		else this.onreset?.();
	}
}
