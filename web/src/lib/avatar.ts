// Generated logos for everything that has no picture of its own: bots,
// containers, workspaces, sites, people and one-click apps without an icon.
// DiceBear draws them in the browser from a stable seed (normally the item's
// ID, so renaming keeps the picture), so nothing is fetched from a third
// party. A logo or avatar someone uploaded always wins over these.
//
// Styles: Bottts Neutral by Pablo Stanley (free for personal and commercial
// use); Disco, Shapes, Glass and Thumbs by DiceBear (CC0 1.0).
import { Avatar, Style } from '@dicebear/core';
import bottts from '@dicebear/styles/bottts-neutral.json';
import disco from '@dicebear/styles/disco.json';
import glass from '@dicebear/styles/glass.json';
import shapes from '@dicebear/styles/shapes.json';
import thumbs from '@dicebear/styles/thumbs.json';

export type AvatarKind = 'bot' | 'container' | 'workspace' | 'site' | 'user' | 'app';

type Definition = ConstructorParameters<typeof Style>[0];
const definitions: Record<AvatarKind, unknown> = {
	bot: bottts,
	container: shapes,
	workspace: disco,
	site: glass,
	user: thumbs,
	app: shapes
};

const styles = new Map<AvatarKind, Style>();
const cache = new Map<string, string>();

/** A data URI of the generated SVG for this kind of item and seed. */
export function avatarUri(kind: AvatarKind, seed: string): string {
	const key = `${kind}\u0000${seed}`;
	let uri = cache.get(key);
	if (uri) return uri;
	let style = styles.get(kind);
	if (!style) {
		style = new Style(definitions[kind] as Definition);
		styles.set(kind, style);
	}
	uri = new Avatar(style, { seed }).toDataUri();
	cache.set(key, uri);
	return uri;
}
