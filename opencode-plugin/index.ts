// super-badger's opencode (>= 2.2) server plugin.
//
// 1. Context limits. opencode gives a custom model it knows nothing about a
//    200k context, so it never compacts in time on a 32k local engine and the
//    request overflows. Every badger provider points at a llama.cpp or Strata
//    server - which one can change at runtime, and so can its context
//    (`nixllm context N`, `nixstrata context N`). Both answer GET /props with
//    default_generation_settings.n_ctx; this plugin probes that and sets each
//    provider's models' limit.context / limit.output, reloading the model
//    registry only when a value changes. super-badger's per-model overrides
//    (GET <superbadgerURL>/internal/limits) win over detection.
//
// 2. Compaction that can't get stuck. opencode checks its summary against a
//    template; a summary that misses it fails the compaction, and every later
//    prompt retries and fails the same way. opencode's own summary goes first
//    (it reuses the prompt cache, which matters on local engines). When it
//    fails, this plugin compacts the session again at once, writing that
//    summary itself: a tool-less generate call over the flattened transcript,
//    trimmed to fit, with a template heading guaranteed (a hook-supplied
//    summary is recorded as-is). If even that fails, super-badger rolls the
//    station over to a new session.
//
// Options (opencode.json `plugins: [{ package, options }]`), all optional:
//   superbadgerURL   e.g. "http://127.0.0.1:8081" - per-model overrides
//   tokenFile        file holding the bearer token for /internal/limits
//   providers        provider IDs to probe (default: every openai-compatible one but "opencode")
//   probeIntervalMs  default 10000
//   maxOutput        cap for the derived output limit, default 8192
import { Plugin } from "@opencode/plugin"
import { readFileSync } from "node:fs"
import {
  type Overrides,
  PROBE_TIMEOUT_MS,
  cachedContext,
  SUMMARY_PROMPT,
  debug,
  effectiveLimit,
  ensureTemplate,
  fitTranscript,
  flatten,
  probeContext,
  rows,
  shouldProbe,
} from "./lib.ts"

export default Plugin.define({
  id: "superbadger",
  async setup(ctx) {
    const opts = (ctx.options ?? {}) as Record<string, unknown>
    const interval = Number(opts.probeIntervalMs) > 0 ? Number(opts.probeIntervalMs) : 10_000
    const maxOutput = Number(opts.maxOutput) > 0 ? Number(opts.maxOutput) : 8_192
    const sbURL = typeof opts.superbadgerURL === "string" ? opts.superbadgerURL.replace(/\/$/, "") : undefined
    const tokenFile = typeof opts.tokenFile === "string" ? opts.tokenFile : undefined
    const only = Array.isArray(opts.providers) ? new Set(opts.providers.map(String)) : undefined

    // ---- limits ----

    let byProvider = new Map<string, number | undefined>() // provider -> detected n_ctx
    let overrides: Overrides = {}
    let lastStatus = 200 // of the last /internal/limits answer

    const loadOverrides = async (): Promise<Overrides> => {
      if (!sbURL) return {}
      try {
        const token = tokenFile ? readFileSync(tokenFile, "utf8").trim() : undefined
        const res = await fetch(`${sbURL}/internal/limits`, {
          signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
          headers: token ? { authorization: `Bearer ${token}` } : undefined,
        })
        if (!res.ok) {
          // Logged once per status, not every poll: a 403 here means
          // super-badger doesn't see this request as coming from its own host,
          // so overrides set in the app never apply.
          if (res.status !== lastStatus) console.error(`superbadger: GET ${sbURL}/internal/limits: HTTP ${res.status}; keeping current overrides`)
          lastStatus = res.status
          return overrides
        }
        lastStatus = res.status
        const body: any = await res.json()
        return body?.overrides && typeof body.overrides === "object" ? body.overrides : {}
      } catch {
        return overrides // super-badger down: keep what we had
      }
    }

    const detect = async () => {
      const next = new Map<string, number | undefined>()
      await Promise.all(
        rows(await ctx.provider.list()).map(async (p: any) => {
          const baseURL = p?.settings?.baseURL
          if (typeof baseURL !== "string" || !shouldProbe(p, only)) return
          const apiKey = typeof p.settings.apiKey === "string" ? p.settings.apiKey : undefined
          next.set(p.id, await probeContext(baseURL, interval / 2, apiKey))
        }),
      )
      return next
    }

    await ctx.model.transform((editor: any) => {
      for (const m of editor.list()) {
        const detected = byProvider.get(m.providerID) ?? cachedContext(m.settings?.baseURL)
        const limit = effectiveLimit(detected, overrides[`${m.providerID}/${m.id}`], maxOutput)
        if (!limit) continue
        editor.update(m.providerID, m.id, (d: any) => {
          d.limit = { ...d.limit, ...limit }
        })
      }
    })

    const refresh = async () => {
      const [detected, ov] = await Promise.all([detect(), loadOverrides()])
      const changed =
        JSON.stringify([...detected].sort()) !== JSON.stringify([...byProvider].sort()) ||
        JSON.stringify(ov) !== JSON.stringify(overrides)
      byProvider = detected
      overrides = ov
      if (!changed) return
      debug("limits changed", { detected: [...detected], overrides: ov })
      await ctx.model.reload()
    }
    const refreshLogged = () => void refresh().catch((e) => console.error("superbadger: limits refresh failed", e))
    let soon: ReturnType<typeof setTimeout> | undefined
    const scheduleRefresh = () => {
      if (soon) return
      soon = setTimeout(() => {
        soon = undefined
        refreshLogged()
      }, 250)
    }
    await refresh().catch((e) => console.error("superbadger: limits refresh failed", e))
    // Config-defined providers finish registering shortly after setup without
    // a provider event; catch them in the first seconds, not an interval later.
    const early = [500, 1_000, 2_000, 4_000].map((ms) => setTimeout(scheduleRefresh, ms))
    const timer = setInterval(refreshLogged, interval)

    // ---- compaction ----

    const failed = new Set<string>() // last compaction failed: the next one is written below
    const healing = new Set<string>() // a fallback compaction was requested since the last success
    const events = new AbortController()
    void (async () => {
      try {
        for await (const ev of (ctx.event as any).subscribe({
          types: ["session.compaction.failed", "session.compaction.ended", "provider.updated", "model.updated"],
          signal: events.signal,
        })) {
          if (ev?.type === "provider.updated" || ev?.type === "model.updated") {
            scheduleRefresh() // refresh() only reloads on a change, so its own reload doesn't loop
            continue
          }
          const sid = ev?.data?.sessionID
          if (!sid) continue
          if (ev.type === "session.compaction.ended") {
            failed.delete(sid)
            healing.delete(sid)
            continue
          }
          // compaction.failed: retry at once through the fallback, so the
          // session heals without waiting for the next prompt. A failure of
          // that retry is left to super-badger.
          failed.add(sid)
          if (healing.has(sid)) continue
          healing.add(sid)
          debug("compaction failed; compacting again via fallback", sid)
          ;(ctx.session as any).compact({ sessionID: sid }).catch((e: unknown) => console.error("superbadger: heal compact failed", e))
        }
      } catch (e) {
        if (!events.signal.aborted) console.error("superbadger: event subscription ended", e)
      }
    })()

    await (ctx.session as any).hook("compaction", async (event: any) => {
      if (!failed.has(event.sessionID)) return // opencode's own summary first
      const model = rows(await ctx.model.list()).find((m) => m.providerID === event.model.providerID && m.id === event.model.id)
      const window = Number(model?.limit?.context) || 32_768
      const output = Math.min(Number(model?.limit?.output) || 4_096, 4_096)
      const budget = Math.max(1_024, window - output - Math.ceil(SUMMARY_PROMPT.length / 3) - 1_024)
      const transcript = fitTranscript(flatten(event.messages), budget)
      let text: string
      try {
        const res: any = await (ctx as any).experimental.generate.text({
          model: { providerID: event.model.providerID, id: event.model.id },
          prompt: `${SUMMARY_PROMPT}\n\n<transcript>\n${transcript}\n</transcript>`,
        })
        text = String(res?.text ?? "")
      } catch (e) {
        // The model call itself failed: leave event.result unset. opencode
        // then makes its own summary request; if that fails too, super-badger
        // rolls the station over.
        console.error("superbadger: fallback summary failed", e)
        return
      }
      debug("fallback summary written", event.sessionID, text.length)
      event.result = { summary: ensureTemplate(text), metadata: { superbadger: "fallback-summary" } }
    })

    return () => {
      clearInterval(timer)
      early.forEach(clearTimeout)
      if (soon) clearTimeout(soon)
      events.abort()
    }
  },
})
