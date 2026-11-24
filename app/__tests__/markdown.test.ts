import {parseMarkdown, splitTableRow, tableColumnWidths, TableSegment, ParagraphSegment} from '../src/markdown';

const text = (runs: {value: string}[]) => runs.map(r => r.value).join('');

// Shape taken from a real opencode reply.
const REPLY = [
  'Coordinates:',
  '',
  '| Key | Range | Meaning |',
  '|---|:-:|---:|',
  '| `x` | `[-1, 1]` | Horizontal offset of the box centre. `0` = frame centre |',
  '| `lost` | bool | **true** = no detection |',
  '| short |',
  '',
  'After.',
].join('\n');

describe('markdown tables', () => {
  it('parses a GFM pipe table between paragraphs', () => {
    const segs = parseMarkdown(REPLY);
    expect(segs.map(s => s.kind)).toEqual(['paragraph', 'table', 'paragraph']);
    const t = segs[1] as TableSegment;
    expect(t.header.map(text)).toEqual(['Key', 'Range', 'Meaning']);
    expect(t.align).toEqual(['left', 'center', 'right']);
    expect(t.rows).toHaveLength(3);
    expect(t.rows[0][0][0]).toMatchObject({code: true, value: 'x'});
    expect(t.rows[1][2][0]).toMatchObject({bold: true, value: 'true'});
    // Ragged rows are padded out to the header's column count.
    expect(t.rows[2].map(text)).toEqual(['short', '', '']);
  });

  it('stays a paragraph until the separator line has streamed in', () => {
    expect(parseMarkdown('| Key | Range |').map(s => s.kind)).toEqual(['paragraph']);
    expect(parseMarkdown('| Key | Range |\n|---|---|').map(s => s.kind)).toEqual(['table']);
  });

  it('ignores pipe tables inside code fences', () => {
    expect(parseMarkdown('```\n| a | b |\n|---|---|\n```').map(s => s.kind)).toEqual(['code-block']);
  });

  it('does not split on pipes inside code spans or escaped pipes', () => {
    expect(splitTableRow('| `a | b` | c \\| d |')).toEqual(['`a | b`', 'c | d']);
  });

  it('gives each column one shared, clamped width', () => {
    const t = parseMarkdown(REPLY)[1] as TableSegment;
    const w = tableColumnWidths(t);
    expect(w).toHaveLength(3);
    expect(w[1]).toBeGreaterThanOrEqual(48);
    expect(w[2]).toBe(240); // the long Meaning cell wraps in a capped column
  });
});

describe('markdown links', () => {
  const runsOf = (s: string) => (parseMarkdown(s)[0] as ParagraphSegment).runs;
  const para = (s: string) => text(runsOf(s));
  const hrefs = (s: string) => runsOf(s).filter(r => r.href).map(r => r.href);

  it('shows [text](url) as "text (url)" with the URL tappable', () => {
    expect(para('See [the docs](https://example.com/a_b_c) now.')).toBe('See the docs (https://example.com/a_b_c) now.');
    expect(hrefs('See [the docs](https://example.com/a_b_c) now.')).toEqual(['https://example.com/a_b_c']);
    expect(para('![logo](https://x.io/i.png "Logo")')).toBe('logo (https://x.io/i.png)');
    expect(para('[wiki](https://en.wikipedia.org/wiki/Foo_(bar))')).toBe('wiki (https://en.wikipedia.org/wiki/Foo_(bar))');
  });

  it('shows just the URL when the label is empty or the URL itself', () => {
    expect(para('[](https://x.io)')).toBe('https://x.io');
    expect(para('[https://x.io](https://x.io)')).toBe('https://x.io');
  });

  it('makes bare URLs tappable and keeps emphasis markers inside them literal', () => {
    const runs = runsOf('go to https://x.io/_a_/*b*. then');
    expect(runs.some(r => r.italic)).toBe(false);
    expect(text(runs)).toBe('go to https://x.io/_a_/*b*. then');
    expect(hrefs('go to https://x.io/_a_/*b*. then')).toEqual(['https://x.io/_a_/*b*']);
  });
});
