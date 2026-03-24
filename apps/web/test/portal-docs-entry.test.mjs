import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('portal opens /api/docs in a new tab and does not render the embedded docs panel', async () => {
  const page = await readFile(new URL('../pages/index.vue', import.meta.url), 'utf8')

  assert.match(page, /href="\/api\/docs"/)
  assert.match(page, /target="_blank"/)
  assert.doesNotMatch(page, /<DocsPanel\s*\/>/)
  assert.doesNotMatch(page, /to="\/docs"/)
})
