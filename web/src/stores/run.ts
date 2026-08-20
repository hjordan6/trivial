import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '../api'
import type { Answer, Outcome, Puzzle, Run, RunEnvelope, Stage, Stats } from '../types'

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
  const remainingMs = computed(() => run.value ? Math.max(0, Date.parse(run.value.expires_at) - (now.value + serverOffset.value)) : 0)
  const complete = computed(() => Boolean(run.value?.completed_at))
  const score = computed(() => run.value?.answers.filter(a => a.outcome === 'star' || a.outcome === 'circle').length ?? 0)

  function hydrate(data:RunEnvelope) {
		puzzle.value = data.run?.puzzle ?? data.puzzle
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
  function shareText(url:string){const rank={easy:0,medium:1,hard:2};const rows=[0,1,2].map(pos=>puzzle.value!.questions.filter(q=>q.topic_position===pos).sort((a,b)=>rank[a.difficulty]-rank[b.difficulty]).map(q=>symbol(answerMap.value.get(q.question_id)?.outcome)).join(''));const elapsed=run.value?.completed_at?Math.max(0,Math.round((Date.parse(run.value.completed_at)-Date.parse(run.value.started_at))/1000)):0;return `Trivial ${puzzle.value!.date}\n${rows.join('\n')}\n${score.value}/9 in ${Math.floor(elapsed/60)}:${String(elapsed%60).padStart(2,'0')}\n${url}`}

  return {puzzle,run,loading,error,stats,answerMap,remainingMs,complete,score,load,start,reveal,answer,finish,share,loadStats,resetForDevelopment,resync,symbol,shareText}
})
