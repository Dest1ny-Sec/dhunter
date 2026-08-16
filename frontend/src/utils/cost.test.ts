import { describe, expect, it } from 'vitest'
import { estimateCostCNY, fmtCostCNY } from './cost'

describe('estimateCostCNY', () => {
  it('returns 0 for an empty run', () => {
    expect(estimateCostCNY({}, 'deepseek-chat')).toBe(0)
  })

  it('charges output at the model rate and cache reads cheaply', () => {
    // deepseek: input 2, output 8, cacheRead 0.2 (¥/1M tokens)
    const cost = estimateCostCNY(
      { input_tokens: 1_000_000, output_tokens: 1_000_000, cache_read_input_tokens: 1_000_000 },
      'deepseek-chat',
    )
    expect(cost).toBeCloseTo(2 + 8 + 0.2, 6)
  })

  it('counts cache_creation (new cached context) at full input rate', () => {
    const cost = estimateCostCNY({ cache_creation_input_tokens: 1_000_000 }, 'deepseek-chat')
    expect(cost).toBeCloseTo(2, 6)
  })

  it('falls back to default pricing for unknown models', () => {
    const cost = estimateCostCNY({ input_tokens: 1_000_000 }, 'some-unknown-model-xyz')
    expect(cost).toBeCloseTo(5, 6)
  })

  it('never returns a negative cost', () => {
    expect(estimateCostCNY({ input_tokens: -5 }, 'deepseek-chat')).toBeGreaterThanOrEqual(0)
  })
})

describe('fmtCostCNY', () => {
  it('formats sub-cent costs as <¥0.01', () => {
    expect(fmtCostCNY(0.001)).toBe('<¥0.01')
  })
  it('formats normal amounts', () => {
    expect(fmtCostCNY(12.345)).toBe('¥12.35')
  })
})
