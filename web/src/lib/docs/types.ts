// Shapes produced at build time by web/scripts/docs.ts.
export type { Block, Inline, Enforcement, CalloutKind, Heading, PageMeta, CategoryMeta, SearchEntry, Release } from '../../../scripts/docs';
import type { CategoryMeta } from '../../../scripts/docs';

export type DocsManifest = { categories: CategoryMeta[]; order: string[]; aliases: Record<string, string> };
