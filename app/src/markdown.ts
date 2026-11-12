// Markdown rendering for the chat transcript: fenced code blocks (with basic
// syntax highlighting), inline code spans, bold/italic, headings, simple
// lists and GFM pipe tables. Not a spec-complete markdown parser (no
// blockquotes/nested lists/links) — just enough that a typical LLM reply
// renders as an actual document instead of raw markdown source or an
// undifferentiated wall of text.
//
// Tables: opencode passes the model's markdown through untouched, and every
// table in real transcripts is the GFM pipe form —
//   | Key | Range | Meaning |
//   |---|---|---|            (sometimes |:-:| for alignment)
//   | `x` | `[-1, 1]` | ... |
// with outer pipes on every line. Detection is kept cheap because this runs
// over the whole reply on every streaming flush: a line is only considered
// as a table header if its first non-space char is '|', and only then is the
// next line checked against the separator regex. A table mid-stream shows as
// a plain paragraph until its separator line arrives, then flips to a grid.

export interface CodeToken {
  type: 'plain' | 'keyword' | 'string' | 'comment' | 'number';
  text: string;
}

export interface CodeBlockSegment {
  kind: 'code-block';
  lang: string;
  code: string;
  tokens: CodeToken[];
}

export interface InlineRun {
  code: boolean;
  bold: boolean;
  italic: boolean;
  value: string;
}

export interface HeadingSegment {
  kind: 'heading';
  level: number;
  runs: InlineRun[];
}

export interface ListItemSegment {
  kind: 'list-item';
  ordered: boolean;
  runs: InlineRun[];
}

export interface ParagraphSegment {
  kind: 'paragraph';
  runs: InlineRun[];
}

export type TableAlign = 'left' | 'center' | 'right';

export interface TableSegment {
  kind: 'table';
  align: TableAlign[];
  header: InlineRun[][];
  // Every row is padded/truncated to the header's column count, so the
  // renderer can rely on rows[r].length === header.length.
  rows: InlineRun[][][];
}

export type MarkdownSegment = CodeBlockSegment | HeadingSegment | ListItemSegment | ParagraphSegment | TableSegment;

// Deliberately generic rather than per-language: this covers the common
// keywords across JS/TS/Python/Go/Rust/Java/C-family well enough to make
// code readable at a glance, without needing a real grammar per language.
const KEYWORDS = new Set([
  'function', 'return', 'const', 'let', 'var', 'if', 'else', 'for', 'while', 'do',
  'class', 'import', 'export', 'default', 'from', 'as', 'def', 'package', 'func',
  'interface', 'type', 'struct', 'enum', 'public', 'private', 'protected', 'static',
  'async', 'await', 'true', 'false', 'null', 'none', 'nil', 'undefined', 'self', 'this',
  'new', 'try', 'catch', 'finally', 'throw', 'throws', 'switch', 'case', 'break',
  'continue', 'in', 'of', 'extends', 'implements', 'super', 'yield', 'with', 'lambda',
  'void', 'int', 'string', 'bool', 'float', 'double', 'char', 'byte', 'long', 'short',
  'go', 'chan', 'defer', 'range', 'select', 'fn', 'impl', 'mod', 'pub', 'match', 'mut',
  'not', 'and', 'or', 'is', 'elif', 'except', 'raise', 'pass', 'global', 'nonlocal',
]);

// Order matters: comments/strings must be matched whole before their
// contents get a chance to match anything else (e.g. a keyword inside a
// string). # is treated as a comment marker generically (Python/Ruby/shell)
// — it'll misfire on languages that use # for something else, but that's an
// acceptable tradeoff for "basic" highlighting with no per-language grammar.
const CODE_TOKEN_RE =
  /(\/\/[^\n]*)|(#[^\n]*)|(\/\*[\s\S]*?\*\/)|("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`(?:[^`\\]|\\.)*`)|(\b\d+(?:\.\d+)?\b)|([A-Za-z_$][A-Za-z0-9_$]*)/g;

export function highlightCode(code: string, _lang: string): CodeToken[] {
  const tokens: CodeToken[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  CODE_TOKEN_RE.lastIndex = 0;
  while ((m = CODE_TOKEN_RE.exec(code))) {
    if (m.index > last) {
      tokens.push({type: 'plain', text: code.slice(last, m.index)});
    }
    if (m[1] !== undefined || m[2] !== undefined || m[3] !== undefined) {
      tokens.push({type: 'comment', text: m[0]});
    } else if (m[4] !== undefined) {
      tokens.push({type: 'string', text: m[0]});
    } else if (m[5] !== undefined) {
      tokens.push({type: 'number', text: m[0]});
    } else {
      tokens.push({type: KEYWORDS.has(m[0]) ? 'keyword' : 'plain', text: m[0]});
    }
    last = m.index + m[0].length;
  }
  if (last < code.length) {
    tokens.push({type: 'plain', text: code.slice(last)});
  }
  return tokens;
}

const INLINE_RE = /`([^`\n]+)`|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*]+)\*|_([^_]+)_/g;

function parseInline(text: string): InlineRun[] {
  const runs: InlineRun[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  INLINE_RE.lastIndex = 0;
  while ((m = INLINE_RE.exec(text))) {
    if (m.index > last) {
      runs.push({code: false, bold: false, italic: false, value: text.slice(last, m.index)});
    }
    if (m[1] !== undefined) {
      runs.push({code: true, bold: false, italic: false, value: m[1]});
    } else if (m[2] !== undefined || m[3] !== undefined) {
      runs.push({code: false, bold: true, italic: false, value: (m[2] ?? m[3])!});
    } else {
      runs.push({code: false, bold: false, italic: true, value: (m[4] ?? m[5])!});
    }
    last = m.index + m[0].length;
  }
  if (last < text.length) {
    runs.push({code: false, bold: false, italic: false, value: text.slice(last)});
  }
  return runs;
}

// Splits one table line into raw cell strings. Pipes inside `code spans`
// and backslash-escaped \| don't split — LLMs routinely put things like
// `a | b` in a code cell without escaping it.
export function splitTableRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith('|')) s = s.slice(1);
  if (s.endsWith('|') && !s.endsWith('\\|')) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = '';
  let inCode = false;
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (ch === '\\' && s[i + 1] === '|') {
      cur += '|';
      i++;
    } else if (ch === '`') {
      inCode = !inCode;
      cur += ch;
    } else if (ch === '|' && !inCode) {
      cells.push(cur.trim());
      cur = '';
    } else {
      cur += ch;
    }
  }
  cells.push(cur.trim());
  return cells;
}

const TABLE_SEP_RE = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;

function isTableStart(line: string, next: string | undefined): boolean {
  // Cheap gate first: the only per-line cost for non-table text.
  if (next === undefined || line.trimStart()[0] !== '|') return false;
  return next.includes('|') && TABLE_SEP_RE.test(next);
}

function parseAlign(cell: string): TableAlign {
  const l = cell.startsWith(':');
  const r = cell.endsWith(':');
  return l && r ? 'center' : r ? 'right' : 'left';
}

// Rough rendered width of a cell, in "average characters" — code is
// monospace and a bit wider than the proportional body font.
function runChars(runs: InlineRun[]): number {
  let n = 0;
  for (const run of runs) n += run.code ? run.value.length * 1.15 : run.value.length;
  return n;
}

// Pixel width for each column, shared by every row so the grid lines up
// like a spreadsheet. Sized to the column's widest cell, clamped so a very
// long cell wraps inside a capped column instead of making one giant row.
export function tableColumnWidths(
  seg: TableSegment,
  {charWidth = 7, padding = 16, min = 48, max = 240} = {},
): number[] {
  return seg.header.map((h, c) => {
    let chars = runChars(h) * 1.08; // header is bold
    for (const row of seg.rows) chars = Math.max(chars, runChars(row[c]));
    return Math.round(Math.min(max, Math.max(min, chars * charWidth + padding)));
  });
}

const FENCE_OPEN_RE = /^```(\S*)\s*$/;
const FENCE_CLOSE_RE = /^```\s*$/;
const HEADING_RE = /^(#{1,6})\s+(.*)$/;
const LIST_RE = /^\s*([-*+]|\d+\.)\s+(.*)$/;

export function parseMarkdown(text: string): MarkdownSegment[] {
  const segments: MarkdownSegment[] = [];
  const lines = text.split('\n');
  let paraBuffer: string[] = [];

  const flushPara = () => {
    if (paraBuffer.length) {
      segments.push({kind: 'paragraph', runs: parseInline(paraBuffer.join('\n'))});
      paraBuffer = [];
    }
  };

  let i = 0;
  while (i < lines.length) {
    const line = lines[i];

    const fenceMatch = FENCE_OPEN_RE.exec(line);
    if (fenceMatch) {
      flushPara();
      const lang = fenceMatch[1] ?? '';
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !FENCE_CLOSE_RE.test(lines[i])) {
        codeLines.push(lines[i]);
        i++;
      }
      i++; // skip closing fence (or EOF if the block was never closed)
      const code = codeLines.join('\n');
      segments.push({kind: 'code-block', lang, code, tokens: highlightCode(code, lang)});
      continue;
    }

    if (isTableStart(line, lines[i + 1])) {
      flushPara();
      const headerCells = splitTableRow(line);
      const cols = headerCells.length;
      const sepCells = splitTableRow(lines[i + 1]);
      const align: TableAlign[] = [];
      for (let c = 0; c < cols; c++) align.push(parseAlign(sepCells[c] ?? ''));
      const rows: InlineRun[][][] = [];
      i += 2;
      while (i < lines.length && lines[i].trimStart()[0] === '|') {
        const cells = splitTableRow(lines[i]);
        const row: InlineRun[][] = [];
        for (let c = 0; c < cols; c++) row.push(parseInline(cells[c] ?? ''));
        rows.push(row);
        i++;
      }
      segments.push({kind: 'table', align, header: headerCells.map(parseInline), rows});
      continue;
    }

    const headingMatch = HEADING_RE.exec(line);
    if (headingMatch) {
      flushPara();
      segments.push({kind: 'heading', level: headingMatch[1].length, runs: parseInline(headingMatch[2])});
      i++;
      continue;
    }

    const listMatch = LIST_RE.exec(line);
    if (listMatch) {
      flushPara();
      segments.push({kind: 'list-item', ordered: /^\d+\.$/.test(listMatch[1]), runs: parseInline(listMatch[2])});
      i++;
      continue;
    }

    if (line.trim() === '') {
      flushPara();
      i++;
      continue;
    }

    paraBuffer.push(line);
    i++;
  }
  flushPara();
  return segments;
}
