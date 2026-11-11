import { describe, expect, test } from "bun:test"
import { contextFromProps, effectiveLimit, ensureTemplate, fitTranscript, flatten, hasTemplateHeading, propsURL, shouldProbe } from "./lib.ts"

describe("propsURL", () => {
  test("strips /v1", () => {
    expect(propsURL("http://nixllm:8080/v1")).toBe("http://nixllm:8080/props")
    expect(propsURL("https://llm.acanavan.com/v1/")).toBe("https://llm.acanavan.com/props")
    expect(propsURL("http://192.168.2.149:8091")).toBe("http://192.168.2.149:8091/props")
  })
  test("bad URL", () => expect(propsURL("not a url")).toBeUndefined())
})

describe("contextFromProps", () => {
  test("llama.cpp", () => expect(contextFromProps({ default_generation_settings: { n_ctx: 65536 } })).toBe(65536))
  test("Strata", () => expect(contextFromProps({ default_generation_settings: { n_ctx: 32768, params: {} } })).toBe(32768))
  test("Strata starting reports 0", () => expect(contextFromProps({ default_generation_settings: { n_ctx: 0 } })).toBeUndefined())
  test("missing", () => expect(contextFromProps({})).toBeUndefined())
})

describe("shouldProbe", () => {
  const nixllm = { id: "home-nixllm-a", package: "@opencode/ai/providers/openai-compatible" }
  test("self-hosted openai-compatible", () => expect(shouldProbe(nixllm)).toBe(true))
  test("hosted opencode provider", () => expect(shouldProbe({ id: "opencode", package: "@opencode/ai/providers/openai-compatible" })).toBe(false))
  test("other packages", () => expect(shouldProbe({ id: "anthropic", package: "@opencode/ai/providers/anthropic" })).toBe(false))
  test("allowlist wins", () => {
    expect(shouldProbe(nixllm, new Set(["nixllm"]))).toBe(false)
    expect(shouldProbe({ id: "nixllm" }, new Set(["nixllm"]))).toBe(true)
  })
})

describe("effectiveLimit", () => {
  test("detected only: output is a quarter, capped", () => {
    expect(effectiveLimit(32768, undefined, 8192)).toEqual({ context: 32768, output: 8192 })
    expect(effectiveLimit(16384, undefined, 8192)).toEqual({ context: 16384, output: 4096 })
    expect(effectiveLimit(262144, undefined, 8192)).toEqual({ context: 262144, output: 8192 })
  })
  test("override wins per field", () => {
    expect(effectiveLimit(32768, { context: 8192 }, 8192)).toEqual({ context: 8192, output: 2048 })
    expect(effectiveLimit(32768, { output: 1000 }, 8192)).toEqual({ context: 32768, output: 1000 })
    expect(effectiveLimit(undefined, { context: 20000, output: 4000 }, 8192)).toEqual({ context: 20000, output: 4000 })
  })
  test("output never fills the window", () => expect(effectiveLimit(4096, { output: 9000 }, 8192)).toEqual({ context: 4096, output: 3840 }))
  test("nothing known", () => expect(effectiveLimit(undefined, undefined, 8192)).toBeUndefined())
})

describe("template", () => {
  test("heading detection matches opencode's line check", () => {
    expect(hasTemplateHeading("## Objective\n- x")).toBe(true)
    expect(hasTemplateHeading("intro\n  ## Next Move  \n1. y")).toBe(true)
    expect(hasTemplateHeading("## Objectives\n### Objective")).toBe(false)
  })
  test("ensureTemplate keeps good summaries", () => expect(ensureTemplate("## Objective\n- x\n")).toBe("## Objective\n- x"))
  test("ensureTemplate wraps bad ones", () => {
    const out = ensureTemplate("Sure! lots happened.")
    expect(hasTemplateHeading(out)).toBe(true)
    expect(out).toContain("Sure! lots happened.")
    expect(hasTemplateHeading(ensureTemplate(""))).toBe(true)
  })
})

describe("flatten / fitTranscript", () => {
  // opencode 2's request messages (packages/ai/src/schema/messages.ts).
  const msgs = [
    { role: "user", content: [{ type: "text", text: "fix the bug" }] },
    {
      role: "assistant",
      content: [
        { type: "reasoning", text: "thinking..." },
        { type: "text", text: "looking" },
        { type: "tool-call", id: "c1", name: "read", input: { path: "a.go" } },
        { type: "tool-call", id: "c2", name: "bash", input: { command: "go test" } },
      ],
    },
    {
      role: "tool",
      content: [
        { type: "tool-result", id: "c1", name: "read", result: { type: "text", value: "package a" } },
        { type: "tool-result", id: "c2", name: "bash", result: { type: "error", value: "exit 1" } },
      ],
    },
    { role: "tool", content: [{ type: "tool-result", id: "c3", name: "grep", result: { type: "content", value: [{ type: "text", text: "a.go:1" }] } }] },
    { role: "tool", content: [{ type: "tool-result", id: "c4", name: "list", result: { type: "json", value: { n: 2 } } }] },
  ]
  test("flatten drops reasoning, notes tools by name", () => {
    expect(flatten(msgs)).toEqual([
      "user: fix the bug",
      'assistant: looking\n[called read {"path":"a.go"}]\n[called bash {"command":"go test"}]',
      "tool: [read result: package a]\n[bash error: exit 1]",
      "tool: [grep result: a.go:1]",
      'tool: [list result: {"n":2}]',
    ])
  })
  test("flatten reads a string content", () => {
    expect(flatten([{ role: "user", content: "hi" }])).toEqual(["user: hi"])
  })

  test("fitTranscript keeps the oldest entries, drops the newest", () => {
    const entries = ["a".repeat(300), "b".repeat(300), "c".repeat(300)] // ~101 tokens each
    const out = fitTranscript(entries, 250)
    expect(out).toContain("a".repeat(300))
    expect(out).toContain("b".repeat(300))
    expect(out).not.toContain("c".repeat(300))
    expect(out).toContain("[1 later messages omitted")
  })
  test("fitTranscript always keeps the previous summary first", () => {
    const cp = "user: <conversation-checkpoint>\n<summary>\nOLD SUMMARY\n</summary>\n</conversation-checkpoint>"
    const out = fitTranscript([cp, "a".repeat(300), "b".repeat(300)], 150)
    expect(out.startsWith(cp)).toBe(true)
    expect(out).toContain("a".repeat(10))
    expect(out).not.toContain("b".repeat(10))
  })
  test("fitTranscript clips a huge previous summary to half the budget, keeping its start", () => {
    const cp = "user: <conversation-checkpoint>\n<summary>\n" + "s".repeat(3000)
    const out = fitTranscript([cp, "a".repeat(30)], 200)
    expect(out.startsWith("user: <conversation-checkpoint>")).toBe(true)
    expect(out).toContain("a".repeat(30))
    expect(out.length).toBeLessThan(200 * 3)
  })
  test("fitTranscript keeps the start of one huge entry", () => {
    const out = fitTranscript(["x".repeat(2000) + "y".repeat(2000)], 200)
    expect(out.startsWith("x".repeat(100))).toBe(true)
    expect(out).not.toContain("y")
    expect(out).not.toContain("omitted") // clipped, not dropped
  })
  test("fitTranscript keeps everything that fits", () => {
    expect(fitTranscript(["a", "b"], 100)).toBe("a\n\nb")
  })
})
