import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api'
import type { AdminDay, AdminGenerateResult, AdminImportResult, AdminQuestion,
  AdminQuestionPage, AdminTopic, APIError } from '../types'

export const HORIZON_DAYS = 14
export const QUESTION_PAGE = 50

export const useAdminStore = defineStore('admin', () => {
  // authed starts null, meaning "not asked yet". The admin cookie is HttpOnly,
  // so the only way to know is to ask the server.
  const authed = ref<boolean|null>(null)
  const days = ref<AdminDay[]>([])
  const topics = ref<AdminTopic[]>([])
  const questions = ref<AdminQuestion[]>([])
  const questionTotal = ref(0)
  const questionOffset = ref(0)
  const questionsLoading = ref(false)
  const filters = ref({topic:'', difficulty:'', search:''})
  const loading = ref(false)
  const error = ref('')
  const notice = ref('')

  function fail(e:unknown) {
    error.value = (e as APIError)?.message ?? 'Something went wrong.'
  }

  // probe distinguishes "show the panel" from "show the login form". A 404
  // means either not signed in or no ADMIN_PASSWORD on this server; the two
  // are deliberately indistinguishable, so the login copy covers both.
  async function probe() {
    try {
      await api<void>('/api/admin/session')
      authed.value = true
    } catch {
      authed.value = false
    }
  }

  async function login(password:string) {
    error.value = ''
    loading.value = true
    try {
      await api<void>('/api/admin/login', {method:'POST', body:JSON.stringify({password})})
      authed.value = true
      await refresh()
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  }

  async function logout() {
    try { await api<void>('/api/admin/logout', {method:'POST'}) } catch { /* leaving is best effort */ }
    authed.value = false
    days.value = []
    topics.value = []
    questions.value = []
    questionTotal.value = 0
  }

  async function refresh() {
    error.value = ''
    loading.value = true
    try {
      const [puzzles, topicList] = await Promise.all([
        api<{days:AdminDay[]}>(`/api/admin/puzzles?days=${HORIZON_DAYS}`),
        api<{topics:AdminTopic[]}>('/api/admin/topics'),
      ])
      days.value = puzzles.days
      topics.value = topicList.topics
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  }

  // replaceDay swaps one row in place so saving a date does not reorder or
  // reload the whole list under the operator's cursor.
  function replaceDay(day:AdminDay) {
    const at = days.value.findIndex(d => d.date === day.date)
    if (at >= 0) days.value[at] = day
  }

  async function saveSlots(date:string, slugs:(string|null)[]) {
    error.value = ''
    notice.value = ''
    loading.value = true
    try {
      const day = await api<AdminDay>(`/api/admin/puzzles/${date}/topics`, {
        method:'PUT',
        body:JSON.stringify({slots:slugs.map((topic_slug, position) => ({position, topic_slug}))}),
      })
      replaceDay(day)
      notice.value = `${date} rebuilt.`
      return true
    } catch (e) {
      fail(e)
      return false
    } finally {
      loading.value = false
    }
  }

  async function clearSlots(date:string) {
    error.value = ''
    notice.value = ''
    loading.value = true
    try {
      const day = await api<AdminDay>(`/api/admin/puzzles/${date}/topics`, {method:'DELETE'})
      replaceDay(day)
      notice.value = `${date} is automatic again.`
      return true
    } catch (e) {
      fail(e)
      return false
    } finally {
      loading.value = false
    }
  }

  async function generate(daysAhead=HORIZON_DAYS) {
    error.value = ''
    notice.value = ''
    loading.value = true
    try {
      const result = await api<AdminGenerateResult>('/api/admin/puzzles/generate', {
        method:'POST', body:JSON.stringify({days:daysAhead}),
      })
      const parts = [`${result.generated.length} generated`, `${result.skipped.length} already there`]
      if (result.failed.length) parts.push(`${result.failed.length} failed`)
      notice.value = parts.join(' · ')
      // Surface the first failure verbatim: it names the topic and difficulty
      // that ran dry, which is the actionable part.
      if (result.failed.length) error.value = result.failed[0].message
      await refresh()
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  }

  async function updateTopic(slug:string, patch:{selection_weight?:number; active?:boolean}) {
    error.value = ''
    notice.value = ''
    try {
      const updated = await api<AdminTopic>(`/api/admin/topics/${slug}`, {
        method:'PATCH', body:JSON.stringify(patch),
      })
      const at = topics.value.findIndex(t => t.slug === slug)
      if (at >= 0) topics.value[at] = updated
      notice.value = `${updated.name} updated.`
    } catch (e) {
      fail(e)
      await refresh()
    }
  }

  // loadQuestions keeps its own loading flag: the library list reloads on
  // every keystroke of the search box, and borrowing the shared one would
  // grey out the puzzle controls above it each time.
  async function loadQuestions(offset = 0) {
    error.value = ''
    questionsLoading.value = true
    try {
      const params = new URLSearchParams({limit:String(QUESTION_PAGE), offset:String(offset)})
      if (filters.value.topic) params.set('topic', filters.value.topic)
      if (filters.value.difficulty) params.set('difficulty', filters.value.difficulty)
      if (filters.value.search) params.set('search', filters.value.search)
      const page = await api<AdminQuestionPage>(`/api/admin/questions?${params}`)
      questions.value = page.questions
      questionTotal.value = page.total
      questionOffset.value = page.offset
    } catch (e) {
      fail(e)
    } finally {
      questionsLoading.value = false
    }
  }

  // importQuestions posts the pasted text unchanged: the body is the seed file,
  // so what works here is exactly what works in seed/questions.json.
  async function importQuestions(text:string) {
    error.value = ''
    notice.value = ''
    questionsLoading.value = true
    try {
      const result = await api<AdminImportResult>('/api/admin/questions/import',
        {method:'POST', body:text})
      notice.value = `Imported ${result.questions} question${result.questions===1?'':'s'} across ${result.topics} topic${result.topics===1?'':'s'}.`
      // A paste can introduce a topic, so the topic list and the filters that
      // depend on it have to catch up before the listing reloads.
      await refresh()
      await loadQuestions(0)
      return true
    } catch (e) {
      fail(e)
      return false
    } finally {
      questionsLoading.value = false
    }
  }

  return {authed, days, topics, questions, questionTotal, questionOffset, questionsLoading,
    filters, loading, error, notice,
    probe, login, logout, refresh, saveSlots, clearSlots, generate, updateTopic,
    loadQuestions, importQuestions}
})
