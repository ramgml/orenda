/**
 * BlockNote schema for the wiki block editor.
 *
 * Round-trip decision (T62, verified 2026-08-24):
 * BlockNote 0.54 does NOT serialize custom inline content types through
 * `blocksToMarkdown` (the HTML→markdown pipeline only handles built-in
 * text/link marks). Therefore `wikiLink` custom inline content is defined
 * for the schema (so BlockNote accepts it) but the `[[` autocomplete
 * inserts a standard `link` with `href="wiki:<slug>"`. This survives the
 * markdown round-trip naturally: `[title](wiki:slug)` → link block.
 * Wiki link chips are rendered via CSS targeting `a[href^="wiki:"]`.
 */
import { BlockNoteSchema } from '@blocknote/core';
import { createReactInlineContentSpec } from '@blocknote/react';

/**
 * WikiLink custom inline content type. Registered in the schema so
 * BlockNote recognizes it, but the `[[` autocomplete inserts a
 * standard `link` instead (see round-trip note above).
 */
const wikiLinkSpec = createReactInlineContentSpec(
  {
    type: 'wikiLink' as const,
    propSchema: {
      slug: { default: '' },
    },
    content: 'none' as const,
  },
  {
    render: (props) => <span className="wiki-link-chip">[[{props.inlineContent.props.slug}]]</span>,
  },
);

/**
 * Extended schema with the wikiLink custom inline content type.
 * BlockSpecs use defaults — the slash menu filter in BlocksEditor
 * restricts to the postanovka whitelist.
 */
import { defaultBlockSpecs, defaultInlineContentSpecs, defaultStyleSpecs } from '@blocknote/core';

/**
 * Block specs filtered to the postanovka whitelist.
 * Excludes: audio, video, toggleListItem (not in slash menu whitelist).
 */
const {
  paragraph,
  heading,
  bulletListItem,
  numberedListItem,
  checkListItem,
  quote,
  codeBlock,
  table,
  image,
  file,
  divider,
} = defaultBlockSpecs;

export const schema = BlockNoteSchema.create({
  blockSpecs: {
    paragraph,
    heading,
    bulletListItem,
    numberedListItem,
    checkListItem,
    quote,
    codeBlock,
    table,
    image,
    file,
    divider,
  },
  inlineContentSpecs: { ...defaultInlineContentSpecs, wikiLink: wikiLinkSpec },
  styleSpecs: defaultStyleSpecs,
});
