// Writes brotli (.br) and gzip (.gz) copies of the text assets in build/ so
// the Go server can send them as-is (internal/api/static.go picks the variant
// the browser accepts). Compressing once at build time costs no CPU per
// request and lets brotli use its slowest, smallest setting.
import { readdirSync, readFileSync, writeFileSync, statSync } from 'node:fs';
import { join, extname } from 'node:path';
import { brotliCompressSync, gzipSync, constants } from 'node:zlib';

const root = new URL('../build/', import.meta.url).pathname;
const TEXT = new Set(['.js', '.css', '.html', '.svg', '.json', '.webmanifest', '.txt', '.xml', '.map']);
const MIN_BYTES = 1024; // below this the encoding overhead outweighs the saving

let files = 0;
let raw = 0;
let br = 0;
function walk(dir) {
	for (const name of readdirSync(dir)) {
		const path = join(dir, name);
		if (statSync(path).isDirectory()) {
			walk(path);
			continue;
		}
		if (!TEXT.has(extname(name))) continue;
		const data = readFileSync(path);
		if (data.length < MIN_BYTES) continue;
		const b = brotliCompressSync(data, {
			params: { [constants.BROTLI_PARAM_QUALITY]: 11, [constants.BROTLI_PARAM_SIZE_HINT]: data.length }
		});
		const g = gzipSync(data, { level: 9 });
		// Keep a variant only when it saves at least a tenth.
		if (b.length < data.length * 0.9) writeFileSync(path + '.br', b);
		if (g.length < data.length * 0.9) writeFileSync(path + '.gz', g);
		files++;
		raw += data.length;
		br += Math.min(b.length, data.length);
	}
}
walk(root);
console.log(`precompress: ${files} files, ${(raw / 1024).toFixed(0)} KiB -> ${(br / 1024).toFixed(0)} KiB with brotli`);
