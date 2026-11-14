// Marks a form field that an analysis just filled in: one light pass of the
// action colour across the field, then it rests. The pass runs again each
// time `run` changes to a non-zero value (a new analysis), never on typing.
// With reduced motion the field only keeps its static "filled" marker.

const reduced = () =>
  typeof matchMedia === "function" &&
  matchMedia("(prefers-reduced-motion: reduce)").matches;

function sweep(el: HTMLElement) {
  if (reduced() || typeof el.animate !== "function") return;
  const rest = getComputedStyle(el).backgroundColor;
  // The tint washes in with the field and drains out slowly: the eye
  // catches which fields changed without the form flashing.
  el.animate(
    [
      { backgroundColor: rest, boxShadow: "0 0 0 0 transparent" },
      {
        backgroundColor: `color-mix(in srgb, var(--color-action) 16%, ${rest})`,
        boxShadow:
          "0 0 0 3px color-mix(in srgb, var(--color-action) 22%, transparent)",
        offset: 0.18,
      },
      { backgroundColor: rest, boxShadow: "0 0 0 0 transparent" },
    ],
    {
      duration: 1400,
      easing: "cubic-bezier(0.16, 1, 0.3, 1)",
      delay: Number(el.dataset.fillOrder ?? 0) * 60,
    },
  );
}

/** use:autofill={run} — `run` is 0 until the first fill, then a counter. */
export function autofill(el: HTMLElement, run: number) {
  let last = run;
  if (run) requestAnimationFrame(() => sweep(el));
  return {
    update(next: number) {
      if (next && next !== last) sweep(el);
      last = next;
    },
  };
}
