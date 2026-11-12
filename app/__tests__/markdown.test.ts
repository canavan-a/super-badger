import {parseMarkdown, splitTableRow, tableColumnWidths, TableSegment} from '../src/markdown';

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
