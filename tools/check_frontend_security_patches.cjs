#!/usr/bin/env node
// Regressions for GHSA-68fv-2mgg-jv7q and GHSA-g2v6-rqmx-r4w6.
// Run after a frozen frontend install; cap time and V8 old space even if a patch regresses.
const assert = require('node:assert/strict')
const { createRequire } = require('node:module')
const path = require('node:path')
const { spawnSync } = require('node:child_process')
const { performance } = require('node:perf_hooks')

if (process.argv[2] !== '--bounded-child') {
  const result = spawnSync(process.execPath, [
    '--max-old-space-size=128', __filename, '--bounded-child'
  ], { timeout: 5000, encoding: 'utf8', maxBuffer: 1024 * 1024 })
  process.stdout.write(result.stdout || '')
  process.stderr.write(result.stderr || '')
  if (result.error) process.stderr.write(`${result.error.message}\n`)
  process.exit(result.status === 0 && !result.error ? 0 : 1)
}

const frontendRequire = createRequire(path.resolve(__dirname, '../frontend/package.json'))
const vueRequire = createRequire(frontendRequire.resolve('vue'))
const compilerRequire = createRequire(vueRequire.resolve('@vue/compiler-sfc'))
const { SourceMapConsumer, SourceNode } = compilerRequire('source-map-js')
const { ssrRenderAttrs } = vueRequire('@vue/server-renderer')
const versions = {
  vue: frontendRequire('vue/package.json').version,
  renderer: vueRequire('@vue/server-renderer/package.json').version,
  sourceMap: compilerRequire('source-map-js/package.json').version
}
assert.deepEqual(versions, { vue: '3.5.43', renderer: '3.5.43', sourceMap: '1.2.2' })

const flat = { version: 3, sources: ['input.js'], names: [], mappings: 'AAAA', sourcesContent: ['x'] }
const section = (line, column = 0, map = flat) => ({
  version: 3, sections: [{ offset: { line, column }, map }]
})
const started = performance.now()
let checked = 0
for (const value of [-1, 0.5, NaN, Infinity, '1', Number.MAX_SAFE_INTEGER + 1]) {
  assert.throws(() => new SourceMapConsumer(section(value)), /non-negative integers/)
  assert.throws(() => new SourceMapConsumer(section(0, value)), /non-negative integers/)
  checked += 2
}
assert.throws(() => new SourceMapConsumer(section(1e7 + 1)), /must not exceed/)
assert.throws(() => new SourceMapConsumer(section(6e6, 0, section(6e6))), /nested sections/)
checked += 2
const consumer = new SourceMapConsumer(section(1e7))
assert.equal(SourceNode.fromStringWithSourceMap('x\n', consumer).toString(), 'x\n')
checked++
assert.ok(performance.now() - started < 1000, 'source map handling must finish promptly')
checked++

const previousWarn = console.warn
const warnings = []
console.warn = (...args) => warnings.push(args.join(' '))
try {
  assert.equal(ssrRenderAttrs({ id: 'safe', ['x\rautofocus\ronfocus']: 'alert(1)' }), ' id="safe"')
} finally {
  console.warn = previousWarn
}
assert.equal(warnings.length, 1)
assert.match(warnings[0], /unsafe attribute name/)
assert.equal(ssrRenderAttrs({ title: '<safe>' }), ' title="&lt;safe&gt;"')
checked += 4
console.log(JSON.stringify({ passed: true, checks: checked, versions,
  elapsed_ms: Math.round(performance.now() - started), old_space_limit_mib: 128,
  process_timeout_ms: 5000 }))
