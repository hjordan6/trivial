import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { AUDIT_PAGE, useAuditStore } from './audit'
import type { AdminQuestion } from '../types'

// A stand-in library the fake API pages over. Deleting from it is what makes
// the paging arithmetic worth testing: every row after the deleted one shifts
// back by one, so an offset computed from the page size alone would skip a
// question at each page boundary.
let library:AdminQuestion[]

function question(id:number):AdminQuestion {
  return {id, topic_slug:'t', topic_name:'T', difficulty:'easy', difficulty_rating:2,
    prompt:`Q${id}`, answer:`A${id}`, aliases:[`A${id}`], distractors:['a','b','c'],
    status:'active', used_count:0, last_used:null}
}

vi.mock('../api', () => ({
  api: vi.fn(async (path:string, init?:RequestInit) => {
    if (init?.method === 'DELETE') {
      const id = Number(path.split('/').pop())
      library = library.filter(q => q.id !== id)
      return undefined
    }
    if (init?.method === 'PUT') {
      const id = Number(path.split('/').pop())
      const body = JSON.parse(String(init.body))
      const at = library.findIndex(q => q.id === id)
      // The real endpoint takes a whole question and derives the band from the
      // rating, so the fake does the same rather than trusting a sent band.
      library[at] = {...library[at], difficulty_rating:body.difficulty_rating,
        difficulty: body.difficulty_rating >= 8 ? 'hard' : body.difficulty_rating >= 5 ? 'medium' : 'easy',
        status: body.status}
      return library[at]
    }
    const params = new URLSearchParams(path.split('?')[1])
    const offset = Number(params.get('offset'))
    const limit = Number(params.get('limit'))
    return {questions:library.slice(offset, offset+limit), total:library.length, limit, offset}
  }),
}))

beforeEach(() => {
  setActivePinia(createPinia())
  library = Array.from({length: AUDIT_PAGE*2 + 4}, (_, i) => question(i+1))
})

describe('audit store', () => {
  it('steps forward across a page boundary without repeating or skipping', async () => {
    const store = useAuditStore()
    await store.load(0)
    const seen:number[] = []
    for (let i=0; i<AUDIT_PAGE+3; i++) {
      seen.push(store.current!.id)
      await store.next()
    }
    expect(seen).toEqual(Array.from({length: AUDIT_PAGE+3}, (_, i) => i+1))
  })

  it('steps back into the previous page landing on its last question', async () => {
    const store = useAuditStore()
    await store.load(AUDIT_PAGE)
    expect(store.current!.id).toBe(AUDIT_PAGE+1)
    await store.prev()
    expect(store.current!.id).toBe(AUDIT_PAGE)
    expect(store.position).toBe(AUDIT_PAGE)
  })

  it('shows the next question after a delete, and counts one fewer', async () => {
    const store = useAuditStore()
    await store.load(0)
    await store.next()
    expect(store.current!.id).toBe(2)
    await store.remove()
    expect(store.current!.id).toBe(3)
    expect(store.total).toBe(AUDIT_PAGE*2 + 3)
    expect(store.position).toBe(2)
    expect(library.some(q => q.id === 2)).toBe(false)
  })

  // Deleting shifts the library under the loaded window, so the next page has
  // to start where the shrunken window ends rather than a fixed page further on.
  it('does not skip a question when the last of a page is deleted', async () => {
    const store = useAuditStore()
    await store.load(0)
    for (let i=0; i<AUDIT_PAGE-1; i++) await store.next()
    expect(store.current!.id).toBe(AUDIT_PAGE)
    await store.remove()
    expect(store.current!.id).toBe(AUDIT_PAGE+1)
  })

  it('steps back a page when the last question of the library is deleted', async () => {
    const store = useAuditStore()
    library = [question(1), question(2)]
    await store.load(0)
    await store.next()
    await store.remove()
    expect(store.current!.id).toBe(1)
    expect(store.hasNext).toBe(false)
  })

  it('keeps the edited question on screen after a rating change', async () => {
    const store = useAuditStore()
    await store.load(0)
    await store.rate(9)
    expect(store.current!.id).toBe(1)
    expect(store.current!.difficulty_rating).toBe(9)
    expect(store.notice).toBe('Now hard · 9.')
  })

  // Retiring is what a question that has been on a board gets instead of a
  // delete, so it must leave everything except the status alone.
  it('retires a question without touching its rating or its place in the queue', async () => {
    const store = useAuditStore()
    await store.load(0)
    await store.next()
    await store.setStatus('retired')
    expect(store.current!.id).toBe(2)
    expect(store.current!.status).toBe('retired')
    expect(store.current!.difficulty_rating).toBe(2)
    expect(store.total).toBe(AUDIT_PAGE*2 + 4)
    expect(store.notice).toBe('Retired — it will not be picked for a future board.')
  })

  it('makes a retired question active again', async () => {
    const store = useAuditStore()
    await store.load(0)
    await store.setStatus('retired')
    await store.setStatus('active')
    expect(store.current!.status).toBe('active')
    expect(store.notice).toBe('Active — it can be picked again.')
  })

  it('does not write when the status is already what was asked for', async () => {
    const store = useAuditStore()
    await store.load(0)
    expect(await store.setStatus('active')).toBe(false)
    expect(store.notice).toBe('')
  })

  // A rating change must not quietly revive a retired question, which is what
  // sending a hard-coded status instead of the question's own would do.
  it('keeps a retired question retired through a rating change', async () => {
    const store = useAuditStore()
    await store.load(0)
    await store.setStatus('retired')
    await store.rate(9)
    expect(store.current!.status).toBe('retired')
    expect(store.current!.difficulty_rating).toBe(9)
  })

  it('reports an empty library rather than a phantom first question', async () => {
    const store = useAuditStore()
    library = []
    await store.load(0)
    expect(store.current).toBeNull()
    expect(store.position).toBe(0)
    expect(store.hasNext).toBe(false)
    expect(store.hasPrev).toBe(false)
  })
})
