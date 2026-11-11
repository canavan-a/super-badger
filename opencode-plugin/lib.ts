// Pure helpers for the super-badger opencode plugin (index.ts). Kept free of
// @opencode/plugin so `bun test` can load them without the opencode runtime.
import { appendFileSync } from "node:fs"

export type Limit = { context?: number; output?: number }
export type Overrides = Record<string, Limit> // "providerID/modelID" -> limit

export const PROBE_TIMEOUT_MS = 2_000
const MIN_CONTEXT = 1_024

// Plugins load once per location (station directory). Probes are shared across
// those instances so N stations don't each poll every engine.
const probes = new Map<string, { at: number; ctx?: number; pending?: Promise<number | undefined> }>()

/**
 * The engine's context from GET <origin>/props, or undefined. llama.cpp and
 * Strata both answer default_generation_settings.n_ctx there. A failed probe,
 * or Strata's 0 while its engine (re)starts, keeps the last good value.
 */
export async function probeContext(baseURL: string, maxAgeMs: number, apiKey?: string): Promise<number | undefined> {
  const url = propsURL(baseURL)
  if (!url) return undefined
  const cached = probes.get(url)
  if (cached?.pending) return cached.pending
  if (cached && Date.now() - cached.at < maxAgeMs) return cached.ctx
  const pending = (async () => {
    try {
      const res = await fetch(url, {
        signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
        headers: apiKey ? { authorization: `Bearer ${apiKey}` } : undefined,
      })
      if (!res.ok) return cached?.ctx
      return contextFromProps(await res.json()) ?? cached?.ctx
    } catch {
      return cached?.ctx
    }
  })()
  probes.set(url, { at: cached?.at ?? 0, ctx: cached?.ctx, pending })
  const ctx = await pending
  probes.set(url, { at: Date.now(), ctx })
  return ctx
}

/**
 * The last good context any plugin instance probed for this baseURL, without
 * a request. A new location's instance uses it until its own first refresh, so
 * it never starts on opencode's 200k default for an engine already probed.
 */
export function cachedContext(baseURL: unknown): number | undefined {
  if (typeof baseURL !== "string") return undefined
  const url = propsURL(baseURL)
  return url ? probes.get(url)?.ctx : undefined
}

/** n_ctx from a /props body (llama.cpp, Strata), or undefined when absent or not usable yet. */
export function contextFromProps(body: any): number | undefined {
  const n = Number(body?.default_generation_settings?.n_ctx ?? body?.n_ctx)
  return Number.isFinite(n) && n >= MIN_CONTEXT ? n : undefined
}

/** http://host:8080/v1 -> http://host:8080/props (llama.cpp and Strata serve /props at the root). */
export function propsURL(baseURL: string): string | undefined {
  try {
    const u = new URL(baseURL)
    u.pathname = u.pathname.replace(/\/v1\/?$/, "").replace(/\/$/, "") + "/props"
    u.search = ""
    return u.toString()
  } catch {
    return undefined
  }
}

/**
 * Which providers to probe: the `providers` option when given; otherwise every
 * openai-compatible provider except opencode's hosted one - in practice the
 * self-hosted llama.cpp / Strata endpoints configured for badger.
 */
export function shouldProbe(p: { id?: string; package?: string }, only?: Set<string>): boolean {
  if (only) return !!p.id && only.has(p.id)
  return p.id !== "opencode" && typeof p.package === "string" && p.package.includes("openai-compatible")
}

/** The limit to apply: an override wins per field; output defaults to a quarter of the context, capped. */
export function effectiveLimit(detected: number | undefined, override: Limit | undefined, maxOutput: number): Limit | undefined {
  const context = override?.context ?? detected
  if (!context) return override?.output ? { output: override.output } : undefined
  const output = override?.output ?? Math.max(256, Math.min(maxOutput, Math.floor(context / 4)))
  return { context, output: Math.min(output, context - 256) }
}

// ---- compaction fallback -------------------------------------------------

/** opencode 2.2's summary template headings (packages/core/src/session/compaction.ts SUMMARY_TEMPLATE). */
const HEADINGS = ["## Objective", "## Requirements", "## Decisions", "## Work State", "## Next Move", "## Relevant Files", "## Important Context"]

export const SUMMARY_PROMPT = `Summarize the conversation transcript below so another agent can continue the work without it.
Reply with only the summary, as markdown, using exactly these section headings (omit a section if it does not apply):
${HEADINGS.join("\n")}
Under "## Next Move" list what to do next. Under "## Relevant Files" list file paths that matter, with a short reason.
Do not call tools. Do not add anything before the first heading.`

/** True when text has at least one template heading on its own line (opencode's own acceptance check). */
export function hasTemplateHeading(text: string): boolean {
  return text.split("\n").some((line) => HEADINGS.includes(line.trim()))
}

/** A summary opencode will accept: the model's text as-is when it has a heading, else wrapped under one. */
export function ensureTemplate(text: string): string {
  const t = text.trim()
  if (hasTemplateHeading(t)) return t
  return `## Important Context\n${t || "(the model returned no summary; earlier conversation was dropped)"}`
}

/**
 * Flattens opencode 2 request messages (@opencode/ai Message) to "role: text" entries. Reasoning is dropped; tool
 * calls ({type: "tool-call", name, input}) and results ({type: "tool-result", name, result: {type, value}}) become
 * short notes.
 */
export function flatten(messages: any[]): string[] {
  const out: string[] = []
  for (const m of messages ?? []) {
    const role = m?.role ?? "message"
    const parts = typeof m?.content === "string" ? [{ type: "text", text: m.content }] : Array.isArray(m?.content) ? m.content : []
    const chunks: string[] = []
    for (const p of parts) {
      if (p?.type === "text") {
        if (p.text) chunks.push(p.text)
      } else if (p?.type === "tool-call") {
        chunks.push(`[called ${p.name ?? "tool"} ${clip(JSON.stringify(p.input ?? {}), 400)}]`)
      } else if (p?.type === "tool-result") {
        const failed = p.result?.type === "error"
        chunks.push(`[${p.name ?? "tool"} ${failed ? "error" : "result"}: ${clip(resultText(p.result), 800)}]`)
      }
    }
    if (chunks.length) out.push(`${role}: ${chunks.join("\n")}`)
  }
  return out
}

/** A tool result's value as text: text as-is, content parts' text joined, anything else as JSON. */
function resultText(result: any): string {
  const v = result?.value
  if (typeof v === "string") return v
  if (result?.type === "content" && Array.isArray(v)) {
    return v.map((c: any) => (c?.type === "text" ? c.text : `[${c?.type ?? "content"}]`)).join("\n")
  }
  return JSON.stringify(v ?? null) ?? ""
}

/** Characters per token for budgeting. Code and JSON run nearer 3 than 4; overestimating the cost is the safe side. */
const CHARS_PER_TOKEN = 3

const tokens = (s: string) => Math.ceil(s.length / CHARS_PER_TOKEN) + 1

/** True for the entry opencode builds from the previous compaction (to-llm-message.ts). */
export function isCheckpoint(entry: string): boolean {
  return entry.includes("<conversation-checkpoint>")
}

/**
 * The transcript to summarize, within budgetTokens. The previous compaction's summary (the first entry, when
 * there is one) always goes in, since nothing else carries what came before it. Then the oldest entries: opencode
 * keeps the newest turns verbatim beside the new summary, so those are the ones to drop.
 */
export function fitTranscript(entries: string[], budgetTokens: number): string {
  const kept: string[] = []
  let used = 0
  let rest = entries
  if (entries.length > 0 && isCheckpoint(entries[0])) {
    // Up to half the budget; it opens with the summary, so a clip keeps the start.
    const cp = clip(entries[0], Math.floor(budgetTokens / 2) * CHARS_PER_TOKEN)
    kept.push(cp)
    used += tokens(cp)
    rest = entries.slice(1)
  }
  let i = 0
  for (; i < rest.length; i++) {
    const cost = tokens(rest[i])
    if (used + cost > budgetTokens) {
      const room = (budgetTokens - used - 1) * CHARS_PER_TOKEN
      if (room >= 200) {
        kept.push(clip(rest[i], room)) // part of one large entry beats none
        i++
      }
      break
    }
    kept.push(rest[i])
    used += cost
  }
  const dropped = rest.length - i
  return kept.join("\n\n") + (dropped > 0 ? `\n\n[${dropped} later messages omitted; they are kept verbatim after this summary]` : "")
}

function clip(s: string, n: number) {
  return s.length > n ? s.slice(0, n) + "…" : s
}

/** Plugin-context list calls return the client's shape: a bare array or `{ data }`. */
export function rows(x: unknown): any[] {
  if (Array.isArray(x)) return x
  const d = (x as any)?.data
  return Array.isArray(d) ? d : []
}

/** SUPERBADGER_PLUGIN_DEBUG=<file>: append diagnostics there (plugin stdout/stderr may not reach the server log). */
export function debug(...args: unknown[]) {
  const f = process.env.SUPERBADGER_PLUGIN_DEBUG
  if (!f) return
  try {
    appendFileSync(f, `${new Date().toISOString()} ${args.map((a) => (typeof a === "string" ? a : JSON.stringify(a))).join(" ")}\n`)
  } catch {}
}
