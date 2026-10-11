// Resolve the real transitive dependency used by Tailwind, rather than a test alias.
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
import test from 'node:test';
const rootRequire = createRequire(fileURLToPath(new URL('../package.json', import.meta.url)));
const tailwindRequire = createRequire(rootRequire.resolve('tailwindcss'));
const micromatchRequire = createRequire(tailwindRequire.resolve('micromatch'));
const bracesPath = micromatchRequire.resolve('braces');
const braces = micromatchRequire('braces');
const baseline = process.env.BRACES_BASELINE ? rootRequire(process.env.BRACES_BASELINE) : null;
const nested = (n, open = '{', close = '}') => open.repeat(n) + 'a,b' + close.repeat(n);
const rejected = fn => assert.throws(fn, e => e instanceof SyntaxError && /local safety limit \(128\)/.test(e.message));

for (const method of ['parse', 'compile', 'expand', 'stringify']) {
  test(`${method}: deeply nested strings fail before native stack exhaustion`, () => {
    for (const pattern of [nested(2000), nested(2000, '(', ')'), '{('.repeat(1000) + 'a,b' + ')}'.repeat(1000), '{'.repeat(2000) + 'x']) {
      rejected(() => braces[method](pattern));
    }
  });
}

test('root convenience and array APIs retain the guard', () => {
  rejected(() => braces(nested(2000)));
  rejected(() => braces.create(nested(2000), { expand: true }));
  rejected(() => braces(['normal/{a,b}', nested(2000)]));
});

test('options cannot disable the fixed local depth limit', () => {
  for (const opts of [{ maxDepth: Infinity }, { maxDepth: 0 }, { maxLength: Infinity }, { rangeLimit: false }]) {
    rejected(() => braces.compile(nested(2000), opts));
  }
});

test('parser boundary: 127 actual groups pass, 128 fail', () => {
  for (const method of ['parse', 'compile', 'expand', 'stringify']) {
    assert.doesNotThrow(() => braces[method](nested(127)));
    rejected(() => braces[method](nested(128)));
  }
});

test('quoted, bracketed and escaped braces are not counted as AST groups', () => {
  for (const pattern of ['"' + nested(2000) + '"', '[' + nested(2000) + ']', '\\{'.repeat(2000)]) {
    assert.doesNotThrow(() => braces.compile(pattern));
    if (baseline) assert.equal(braces.compile(pattern), baseline.compile(pattern));
  }
});

test('direct AST callers cannot bypass recursive walker guards', () => {
  for (const method of ['compile', 'expand', 'stringify']) {
    // The unpatched parser supplies a genuine AST, including its parent/prev links.
    if (baseline) rejected(() => braces[method](baseline.parse(nested(2000))));
    else {
      const ast = { type: 'root', nodes: [] };
      let parent = ast;
      for (let i = 0; i < 2000; i++) {
        const child = { type: 'paren', nodes: [], parent };
        parent.nodes.push(child);
        parent = child;
      }
      parent.nodes.push({ type: 'text', value: 'x', parent });
      rejected(() => braces[method](ast));
    }
  }
});

test('existing maxLength, range limit and invalid type errors remain', () => {
  assert.throws(() => braces.parse('x'.repeat(10001)), SyntaxError);
  assert.throws(() => braces.parse('abcd', { maxLength: 3 }), SyntaxError);
  assert.throws(() => braces.parse(1), TypeError);
  assert.throws(() => braces.expand('{1..1001}'), /range limit/);
});

test('safe patterns retain known expansion and compilation', () => {
  assert.deepEqual(braces.expand('src/{a,b}/**/*.{ts,vue}'), ['src/a/**/*.ts', 'src/a/**/*.vue', 'src/b/**/*.ts', 'src/b/**/*.vue']);
  assert.deepEqual(braces.expand('{01..03}'), ['01', '02', '03']);
  assert.deepEqual(braces.expand('{a,{b,c}}'), ['a', 'b', 'c']);
  assert.equal(braces.compile('x/{a,b}/y'), 'x/(a|b)/y');
});

test('actual Tailwind micromatch and chokidar use the same guarded package', () => {
  const chokidarRequire = createRequire(tailwindRequire.resolve('chokidar'));
  assert.equal(chokidarRequire.resolve('braces'), bracesPath);
  rejected(() => chokidarRequire('braces').expand(nested(2000)));
  const mm = tailwindRequire('micromatch');
  assert.deepEqual(mm(['src/a.ts', 'src/b.vue', 'src/c.js'], 'src/*.{ts,vue}'), ['src/a.ts', 'src/b.vue']);
  rejected(() => mm.braces(nested(2000)));
});

test('deep input fails controllably in an isolated 512 KiB stack / 64 MiB heap process', () => {
  const code = `const b=require(${JSON.stringify(bracesPath)});try{b.compile('{'.repeat(2000)+'a,b'+'}'.repeat(2000));process.exit(2)}catch(e){if(e.name!=='SyntaxError'||!/local safety limit/.test(e.message))process.exit(3)}`;
  const result = spawnSync(process.execPath, ['--stack_size=512', '--max-old-space-size=64', '-e', code], { timeout: 5000 });
  assert.ifError(result.error);
  assert.equal(result.signal, null);
  assert.equal(result.status, 0, result.stderr?.toString());
});

// Optional local differential lane. CI still executes all guard/integration tests above.
if (baseline) {
  test('unpatched control reproduces the reported native RangeError', () => {
    assert.throws(() => baseline.compile(nested(2000)), e => e instanceof RangeError && /call stack/.test(e.message));
  });
  test('ordinary corpus matches unpatched APIs and error semantics', () => {
    const atoms = ['a', '*', '?', '[ab]', '\\{literal\\}', '"{a,b}"', '${value}', '{a,b}', '{a,{b,c}}', '{01..03}', '{-2..2}', '{z..a}', '{a,b', 'a,b}', '{}', '{,a}', '{a,,b}', '(a|b)', '[{a,b}]', '{1..4..2}'];
    const patterns = [...atoms, ...atoms.flatMap(a => atoms.map(b => `src/${a}/${b}.ts`)), nested(100), nested(127)];
    const options = [{}, { escapeInvalid: true }, { keepEscaping: true }, { keepQuotes: true }, { noempty: true, nodupes: true }, { rangeLimit: 2 }];
    const outcome = fn => { try { return { value: fn() }; } catch (e) { return { error: e.name, message: e.message }; } };
    for (const p of patterns) for (const opts of options) for (const method of ['compile', 'expand', 'stringify']) {
      assert.deepEqual(outcome(() => braces[method](p, opts)), outcome(() => baseline[method](p, opts)), `${method} ${p} ${JSON.stringify(opts)}`);
    }
  });
}
