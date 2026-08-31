import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '../api'
import type { AdminQuestion, AdminQuestionInput, AdminQuestionPage, APIError } from '../types'

// The audit page shows one question at a time, so a page of results is a
// read-ahead buffer rather than a list anyone reads. It is kept small: every
// row past the first is work done on the chance the operator keeps going.
export const AUDIT_PAGE = 25

export const useAuditStore = defineStore('audit', () => {
  // items is the loaded window, offset is where items[0] sits in the filtered
  // library, and index is the card on screen. Position in the whole library is
  // therefore offset + index, which is what the counter reports.
  const items = ref<AdminQuestion[]>([])
  const total = ref(0)
  const offset = ref(0)
  const index = ref(0)
  const loading = ref(false)
  const error = ref('')
  const notice = ref('')
  const filters = ref({topic:'', difficulty:''})

  const current = computed<AdminQuestion|null>(() => items.value[index.value] ?? null)
  const position = computed(() => (total.value ? offset.value + index.value + 1 : 0))
  const hasPrev = computed(() => offset.value + index.value > 0)
  const hasNext = computed(() => offset.value + index.value + 1 < total.value)

  function fail(e:unknown) {
    error.value = (e as APIError)?.message ?? 'Something went wrong.'
  }

  // load fetches the window starting at `at`. Landing on the last card matters
  // when stepping backwards over a page boundary: the operator asked for the
  // previous question, not the first question of the previous page.
  async function load(at:number, land:'first'|'last' = 'first') {
    error.value = ''
    loading.value = true
    try {
      const params = new URLSearchParams({limit:String(AUDIT_PAGE), offset:String(Math.max(0, at))})
      if (filters.value.topic) params.set('topic', filters.value.topic)
      if (filters.value.difficulty) params.set('difficulty', filters.value.difficulty)
      const page = await api<AdminQuestionPage>(`/api/admin/questions?${params}`)
      items.value = page.questions
      total.value = page.total
      offset.value = page.offset
      index.value = land === 'last' ? Math.max(0, page.questions.length - 1) : 0
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  }

  // restart is what a filter change calls: a new filter means a new library to
  // walk, so the cursor goes back to the top of it.
  async function restart() {
    notice.value = ''
    await load(0)
  }

  async function next() {
    notice.value = ''
    if (index.value + 1 < items.value.length) {
      index.value++
      return
    }
    // The next unseen row sits directly after the window, and the window may
    // have shrunk since it was loaded if questions were deleted out of it.
    if (offset.value + items.value.length < total.value) await load(offset.value + items.value.length)
  }

  async function prev() {
    notice.value = ''
    if (index.value > 0) {
      index.value--
      return
    }
    if (offset.value > 0) await load(Math.max(0, offset.value - AUDIT_PAGE), 'last')
  }

  // inputFor rebuilds the whole question with one field changed. The server
  // takes an edit as a replacement rather than a patch, because the rules that
  // matter -- a distractor must not also grade as correct -- can only be
  // checked against a whole question. The stored aliases already carry the
  // canonical answer, and sending it back changes nothing.
  function inputFor(q:AdminQuestion, patch:Partial<AdminQuestionInput>):AdminQuestionInput {
    return {
      topic_slug: q.topic_slug,
      prompt: q.prompt,
      answer: q.answer,
      difficulty_rating: q.difficulty_rating,
      aliases: q.aliases,
      distractors: q.distractors,
      status: q.status,
      ...patch,
    }
  }

  // save is the one write both the rating buttons and the retire button go
  // through, so a change to either lands the same way: the row swapped in
  // place rather than reloaded. Rating and status both feed the listing's sort
  // and filters, so a reload could move this question to another page and pull
  // a stranger onto the screen the moment it was saved.
  async function save(patch:Partial<AdminQuestionInput>, describe:(q:AdminQuestion)=>string) {
    const q = current.value
    if (!q) return false
    error.value = ''
    notice.value = ''
    loading.value = true
    try {
      const updated = await api<AdminQuestion>(`/api/admin/questions/${q.id}`,
        {method:'PUT', body:JSON.stringify(inputFor(q, patch))})
      items.value[index.value] = updated
      notice.value = describe(updated)
      return true
    } catch (e) {
      fail(e)
      return false
    } finally {
      loading.value = false
    }
  }

  async function rate(rating:number) {
    if (rating === current.value?.difficulty_rating) return false
    return save({difficulty_rating: rating}, q => `Now ${q.difficulty} · ${q.difficulty_rating}.`)
  }

  // setStatus is the answer to a question that cannot be deleted because it has
  // been on a board. Retiring stops it being picked for any future board and
  // leaves the ones behind it alone, which is the whole difference between it
  // and a delete.
  //
  // The way back is deliberately "make active" rather than an undo: a retired
  // question that started as a draft would come back as a draft, and a button
  // whose effect depends on history nobody can see is worse than one that says
  // what it does.
  async function setStatus(status:string) {
    if (status === current.value?.status) return false
    return save({status}, q => q.status === 'retired'
      ? 'Retired — it will not be picked for a future board.'
      : 'Active — it can be picked again.')
  }

  async function remove() {
    const q = current.value
    if (!q) return false
    error.value = ''
    notice.value = ''
    loading.value = true
    try {
      await api<void>(`/api/admin/questions/${q.id}`, {method:'DELETE'})
    } catch (e) {
      fail(e)
      loading.value = false
      return false
    }
    loading.value = false
    items.value.splice(index.value, 1)
    total.value = Math.max(0, total.value - 1)
    notice.value = 'Question deleted.'
    // The deleted row is gone from the library too, so everything after it
    // shifted back by one and the next question is already at this index.
    if (index.value < items.value.length) return true
    if (offset.value + items.value.length < total.value) {
      await load(offset.value + items.value.length)
    } else if (!items.value.length && offset.value > 0) {
      // The last question of the last page: step back into the page before it.
      await load(Math.max(0, offset.value - AUDIT_PAGE), 'last')
    } else {
      index.value = Math.max(0, items.value.length - 1)
    }
    return true
  }

  return {items, total, offset, index, loading, error, notice, filters,
    current, position, hasPrev, hasNext,
    load, restart, next, prev, rate, setStatus, remove}
})
