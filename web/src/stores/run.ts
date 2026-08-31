import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '../api'
import type { Answer, Difficulty, Outcome, Puzzle, Question, Run, RunEnvelope, Stage, Stats } from '../types'

// Boards are always laid out topic-by-topic, easiest first, no matter what
// order the server dealt the questions in for this particular run.
export const DIFFICULTY_RANK: Record<Difficulty, number> = { easy: 0, medium: 1, hard: 2 }

// Harder questions are worth more, and typing the answer instead of picking it
// off the list earns the bonus on top.
export const DIFFICULTY_POINTS: Record<Difficulty, number> = { easy: 3, medium: 4, hard: 5 }
export const FREE_TEXT_BONUS = 2
export const MAX_POINTS = 3 * (DIFFICULTY_POINTS.easy + DIFFICULTY_POINTS.medium + DIFFICULTY_POINTS.hard) + 9 * FREE_TEXT_BONUS

// Every share row is labelled with its topic, so a grid alone says what the
// board was about. Both the slugs the seed file builds and the ones in the
// question dump are spelled out here; anything imported later falls back to a
// keyword match on its name, then to a generic marker.
export const TOPIC_EMOJI: Record<string, string> = {
  'geography': '🌍',
  'world-geography': '🌍',
  'science-nature': '🔬',
  'science-and-nature': '🔬',
  'movies-tv': '🎬',
  'film-and-television': '🎬',
  'music': '🎵',
  'sports': '⚽',
  'sport': '⚽',
  'u-s-history': '🗽',
  'world-history': '🏛️',
  'history': '🏛️',
  'art-culture': '🎨',
  'literature-language': '📚',
  'technology-internet': '💻',
  'modern-pop-culture': '✨',
}

const TOPIC_KEYWORD_EMOJI: [RegExp, string][] = [
  [/geograph|\bworld\b|map|travel|countr|capital/i, '🌍'],
  [/scien|nature|biolog|chemi|physic|space|astronom/i, '🔬'],
  [/film|movie|televis|\btv\b|cinema/i, '🎬'],
  [/\bu\.?s\.?\b|america/i, '🗽'],
  [/histor|ancient|war/i, '🏛️'],
  [/music|song|band|album/i, '🎵'],
  [/sport|football|soccer|olymp/i, '⚽'],
  [/pop culture|celebrit|trend|viral|meme/i, '✨'],
  [/tech|comput|internet|softwar|game|gaming/i, '💻'],
  [/literat|languag|book|author|poet|word/i, '📚'],
  [/\bart\b|culture|paint|sculpt|museum/i, '🎨'],
  [/food|drink|cook|cuisine/i, '🍽️'],
  [/politic|govern|law|electio/i, '🗳️'],
  [/animal|wildlife|nature/i, '🐾'],
  [/myth|religio|folklore/i, '🔮'],
  [/business|econom|money|financ/i, '💰'],
]

export function topicEmoji(slug:string, name = ''):string {
  if (TOPIC_EMOJI[slug]) return TOPIC_EMOJI[slug]
  const haystack = `${slug.replace(/-/g, ' ')} ${name}`
  return TOPIC_KEYWORD_EMOJI.find(([pattern]) => pattern.test(haystack))?.[1] ?? '❓'
}

export const useRunStore = defineStore('run', () => {
  const puzzle = ref<Puzzle>()
  const run = ref<Run>()
  const loading = ref(true)
  const error = ref('')
  const serverOffset = ref(0)
  const now = ref(Date.now())
  const stats = ref<Stats>()
  let ticker:number|undefined

  const answerMap = computed(() => new Map((run.value?.answers ?? []).map(a => [a.question_id, a])))
  const questionMap = computed(() => new Map((puzzle.value?.questions ?? []).map(q => [q.question_id, q])))
  const remainingMs = computed(() => run.value ? Math.max(0, Date.parse(run.value.expires_at) - (now.value + serverOffset.value)) : 0)
  const complete = computed(() => Boolean(run.value?.completed_at))
  const score = computed(() => run.value?.answers.filter(a => a.outcome === 'star' || a.outcome === 'circle').length ?? 0)
  const stars = computed(() => run.value?.answers.filter(a => a.outcome === 'star').length ?? 0)
  const points = computed(() => (run.value?.answers ?? []).reduce((total, a) => {
    if (a.outcome !== 'star' && a.outcome !== 'circle') return total
    const question = questionMap.value.get(a.question_id)
    if (!question) return total
    return total + DIFFICULTY_POINTS[question.difficulty] + (a.outcome === 'star' ? FREE_TEXT_BONUS : 0)
  }, 0))

  // One row per topic, each row easy -> medium -> hard. Both the results grid
  // and the share text read the board this way so they always agree, and so
  // every player's grid lines up with everyone else's.
  function orderedRows():Question[][] {
    const questions = puzzle.value?.questions ?? []
    return [0, 1, 2].map(position => questions
      .filter(q => q.topic_position === position)
      .sort((a, b) => DIFFICULTY_RANK[a.difficulty] - DIFFICULTY_RANK[b.difficulty]))
  }

  function hydrate(data:RunEnvelope) {
		puzzle.value = data.run?.puzzle ?? data.puzzle
    // A run with no answers yet comes back as `answers: null`, not `[]`.
    if (data.run && !data.run.answers) data.run.answers = []
    run.value = data.run
    serverOffset.value = Date.parse(data.server_time) - Date.now()
    now.value = Date.now()
  }

  async function load() {
    loading.value = true; error.value = ''
    try { hydrate(await api<RunEnvelope>('/api/runs/current')); if (complete.value) await loadStats() }
    catch (e:any) { error.value = e.message ?? 'Could not load today’s puzzle.' }
    finally { loading.value = false; startTicker() }
  }

  async function start() { hydrate(await api<RunEnvelope>('/api/runs', {method:'POST',body:'{}'})) }
  async function reveal(questionID:number) { return (await api<{options:string[]}>(`/api/runs/${run.value!.id}/questions/${questionID}/reveal-options`,{method:'POST'})).options }
  async function answer(questionID:number, stage:Stage, answer:string) {
    try {
      const resolved = await api<Answer>(`/api/runs/${run.value!.id}/questions/${questionID}/answer`,{method:'POST',body:JSON.stringify({stage,answer})})
      upsert(resolved)
      if (run.value?.answers.filter(a => a.outcome).length === 9) await load()
      return resolved
    } catch (e:any) {
      if (['run_expired','already_answered','invalid_stage'].includes(e.code)) await load()
      throw e
    }
  }
  function upsert(answer:Answer) { if (!run.value) return; const i=run.value.answers.findIndex(a=>a.question_id===answer.question_id); if(i<0)run.value.answers.push(answer);else run.value.answers[i]=answer }
  async function finish() { if(!run.value)return; hydrate(await api<RunEnvelope>(`/api/runs/${run.value.id}/finish`,{method:'POST'})); await loadStats() }
  async function share(nickname?:string) { return api<{token:string;url:string}>(`/api/runs/${run.value!.id}/share`,{method:'POST',body:JSON.stringify({nickname})}) }
  async function loadStats() { stats.value=await api<Stats>('/api/stats') }
  async function resetForDevelopment() {
    await api<void>('/api/dev/reset', {method:'POST'})
    stats.value=undefined
    await load()
  }
  function startTicker(){ if(ticker)return; ticker=window.setInterval(async()=>{now.value=Date.now();if(run.value&&!complete.value&&remainingMs.value===0)await finish()},250) }
  async function resync(){if(run.value&&!complete.value)await load()}
  function symbol(outcome?:Outcome){return ({star:'⭐',circle:'🟢',miss:'🔴',expired:'⏰'} as const)[outcome!] ?? '⬜'}
  function shareText(url:string){
    const rows=orderedRows().filter(row=>row.length).map(row=>`${topicEmoji(row[0].topic_slug,row[0].topic_name)} ${row.map(q=>symbol(answerMap.value.get(q.question_id)?.outcome)).join('')}`)
    const elapsed=run.value?.completed_at?Math.max(0,Math.round((Date.parse(run.value.completed_at)-Date.parse(run.value.started_at))/1000)):0
    return `Trivial ${puzzle.value!.date}\n${rows.join('\n')}\n${score.value}/9 · ${points.value} pts in ${Math.floor(elapsed/60)}:${String(elapsed%60).padStart(2,'0')}\n${url}`
  }

  return {puzzle,run,loading,error,stats,answerMap,remainingMs,complete,score,stars,points,orderedRows,load,start,reveal,answer,finish,share,loadStats,resetForDevelopment,resync,symbol,shareText,topicEmoji}
})
