import { describe, expect, it } from 'vitest'
import * as XLSX from 'xlsx'

// The view tests cover paging/cancellation and row mapping. These tests exercise
// the real dependency and the same incremental APIs used by UsageView exports.
function exportAndRead(sheet: XLSX.WorkSheet): XLSX.WorkSheet {
  const book = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(book, sheet, 'Usage')
  const bytes = XLSX.write(book, { bookType: 'xlsx', type: 'array' })
  expect(Array.from(new Uint8Array(bytes).slice(0, 2))).toEqual([0x50, 0x4b])
  const read = XLSX.read(bytes, { type: 'array', cellNF: true })
  expect(read.SheetNames).toEqual(['Usage'])
  return read.Sheets.Usage
}

describe('UsageView real Excel export contract', () => {
  it('uses the pinned official security release', () => {
    expect(XLSX.version).toBe('0.20.3')
  })

  it('round-trips Chinese, Unicode, quotes and line breaks across export batches', () => {
    const headers = ['时间', '用户', '模型', 'Token数', '客户扣费', '上游成本', '请求ID', '备注']
    const sheet = XLSX.utils.aoa_to_sheet([headers])
    const firstPage = Array.from({ length: 100 }, (_, i) => [
      '2026-10-05T12:00:00Z', '虚构用户@example.invalid', '模型-中文🧪', i,
      '0.007200', '0.001200', `synthetic-${i}`, '引号"、逗号,与\n换行'
    ])
    XLSX.utils.sheet_add_aoa(sheet, firstPage, { origin: -1 })
    XLSX.utils.sheet_add_aoa(sheet, [['2026-10-05T12:01:00Z', '末页用户', '模型二', 0, '0.000000', '0.000000', '末页', '完成']], { origin: -1 })
    const restored = exportAndRead(sheet)
    expect(restored['!ref']).toBe('A1:H102')
    expect(XLSX.utils.sheet_to_json(restored, { header: 1 })).toEqual([
      headers, ...firstPage,
      ['2026-10-05T12:01:00Z', '末页用户', '模型二', 0, '0.000000', '0.000000', '末页', '完成']
    ])
    expect(restored.D2.t).toBe('n')
    expect(restored.E2).toMatchObject({ t: 's', v: '0.007200' })
  })

  it('preserves numeric, boolean, identifier, ISO time, zero and missing-value types', () => {
    const sheet = XLSX.utils.aoa_to_sheet([
      ['tokens', 'enabled', 'request_id', 'created_at', 'cost', 'missing', 'empty'],
      [0, false, '000012345678901234567890', '2026-10-05T12:00:00+08:00', '0.000000', null, '']
    ])
    const restored = exportAndRead(sheet)
    expect(restored.A2).toMatchObject({ t: 'n', v: 0 })
    expect(restored.B2).toMatchObject({ t: 'b', v: false })
    expect(restored.C2).toMatchObject({ t: 's', v: '000012345678901234567890' })
    expect(restored.D2).toMatchObject({ t: 's', v: '2026-10-05T12:00:00+08:00' })
    expect(restored.E2).toMatchObject({ t: 's', v: '0.000000' })
    expect(XLSX.utils.sheet_to_json(restored, { header: 1, defval: '' })[1]).toEqual([
      0, false, '000012345678901234567890', '2026-10-05T12:00:00+08:00', '0.000000', '', ''
    ])
  })

  it.each(['=SUM(A1:A2)', '=HYPERLINK("https://example.invalid","测试")', '+1+1', '-1+1', '@SUM(A1:A2)'])(
    'keeps user-supplied formula-like text literal: %s', (text) => {
      const restored = exportAndRead(XLSX.utils.aoa_to_sheet([['备注'], [text]]))
      expect(restored.A2).toMatchObject({ t: 's', v: text })
      expect(restored.A2.f).toBeUndefined()
    }
  )

  it('preserves an explicitly typed formula, cached result and number format', () => {
    const sheet = XLSX.utils.aoa_to_sheet([['数值', '公式'], [2], [4]])
    sheet.B2 = { t: 'n', f: 'SUM(A2:A3)', v: 6, z: '0.000000' }
    const restored = exportAndRead(sheet)
    expect(restored.B2).toMatchObject({ t: 'n', f: 'SUM(A2:A3)', v: 6, z: '0.000000' })
  })

  it('rejects adversarial unterminated HTML comments promptly', () => {
    const started = performance.now()
    expect(() => XLSX.read('<html><body>' + '<!--'.repeat(60_000), { type: 'string' })).toThrow('Invalid HTML: could not find <table>')
    expect(performance.now() - started).toBeLessThan(1500)
    expect(Object.getPrototypeOf({})).toBe(Object.prototype)
  })
})
