// Rough per-run cost estimate (CNY). Prices are per 1M tokens and are
// approximations for the common models — enough to budget, not billing.
// cache_read is charged much lower than fresh input; cache_write is the
// "new context" bucket at full input rate.

interface Tokens {
  input_tokens?: number
  output_tokens?: number
  cache_read_input_tokens?: number
  cache_creation_input_tokens?: number
}

// model substring -> { input, output, cacheRead } CNY per 1M tokens
const PRICES: Array<{ match: RegExp; input: number; output: number; cacheRead: number }> = [
  { match: /deepseek|deepseek-chat|deepseek-reasoner/i, input: 2, output: 8, cacheRead: 0.2 },
  { match: /minimax|minimax-m/i, input: 3, output: 12, cacheRead: 0.3 },
  { match: /qwen|dashscope/i, input: 1.5, output: 6, cacheRead: 0.15 },
  { match: /glm|zhipu/i, input: 1.5, output: 5, cacheRead: 0.15 },
  { match: /claude/i, input: 20, output: 100, cacheRead: 2 },
  { match: /gpt-4/i, input: 30, output: 120, cacheRead: 3 },
  { match: /gpt-3\.5/i, input: 3, output: 12, cacheRead: 0.3 },
]
const DEFAULT_PRICE = { input: 5, output: 20, cacheRead: 0.5 }

export function estimateCostCNY(t: Tokens, model: string = ''): number {
  const p = PRICES.find((x) => x.match.test(model)) || DEFAULT_PRICE
  const inTok = (t.input_tokens || 0) / 1_000_000
  const outTok = (t.output_tokens || 0) / 1_000_000
  const readTok = (t.cache_read_input_tokens || 0) / 1_000_000
  const writeTok = (t.cache_creation_input_tokens || 0) / 1_000_000
  // cache_write is new context cached at input rate; cache_read is the cheap bucket.
  return Math.max(0, inTok * p.input + outTok * p.output + readTok * p.cacheRead + writeTok * p.input)
}

export function fmtCostCNY(yuan: number): string {
  if (yuan < 0.01) return '<¥0.01'
  return `¥${yuan.toFixed(2)}`
}
