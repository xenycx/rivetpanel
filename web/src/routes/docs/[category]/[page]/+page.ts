import { error } from '@sveltejs/kit';
import { category, pageMeta, loadPage, neighbours } from '$lib/docs';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const slug = `${params.category}/${params.page}`;
	const meta = pageMeta(slug);
	const blocks = meta ? await loadPage(slug) : null;
	if (!meta || !blocks) error(404, 'There is no documentation page at this address.');
	return { meta, blocks, category: category(params.category)!, ...neighbours(slug) };
};
