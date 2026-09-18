<script setup lang="ts">
import { computed, onBeforeMount, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRunStore } from '../stores/run'
import { useAccountStore } from '../stores/account'
import StartScreen from '../components/StartScreen.vue'
import TimerBar from '../components/TimerBar.vue'
import QuestionCard from '../components/QuestionCard.vue'
import ResultsView from '../components/ResultsView.vue'
import SignInGate from '../components/SignInGate.vue'

const store = useRunStore()
const account = useAccountStore()
const currentIndex = ref(0)
const card = ref<InstanceType<typeof QuestionCard>>()
const isLocal = ['localhost', '127.0.0.1', '::1'].includes(location.hostname)
const questions = computed(() => store.puzzle?.questions ?? [])
const currentQuestion = computed(() => questions.value[currentIndex.value])

onBeforeMount(() => { store.load(); account.probe() })
function onVisibilityChange() { if (!document.hidden) store.resync() }
onMounted(() => document.addEventListener('visibilitychange', onVisibilityChange))
onUnmounted(() => document.removeEventListener('visibilitychange', onVisibilityChange))

watch(() => store.run?.id, () => {
  if (!store.run) { currentIndex.value = 0; return }
  const firstUnresolved = questions.value.findIndex(q => !store.answerMap.get(q.question_id)?.outcome)
  currentIndex.value = firstUnresolved < 0 ? 0 : firstUnresolved
})

async function reveal(id:number) {
  card.value?.setOptions(await store.reveal(id))
}
async function answer(id:number, stage:'free_text'|'multiple_choice', value:string) {
  const result = await store.answer(id, stage, value)
  if (stage === 'free_text' && !result.outcome) card.value?.setOptions(await store.reveal(id))
}
// The sign-in gate sits between the last answer and the result, and is asked at
// most once a day: "continue as guest" is remembered under the puzzle date, so
// a refresh, a second tab or coming back this evening does not ask again, while
// tomorrow's result gets a fresh ask.
//
// Keyed by date rather than a single flag because the answer means "not today",
// not "never" -- a player who declines in March should still be offered it in
// April, when they have a streak worth keeping.
function guestKey(date:string) { return `trivial:guest:${date}` }
const guestToday = ref(false)
watch(() => store.puzzle?.date, date => {
  // Private browsing and blocked storage both throw here rather than returning
  // null. Failing to read is treated as "not dismissed", which asks once more
  // than it should rather than silently never asking.
  try { guestToday.value = !!date && localStorage.getItem(guestKey(date)) === '1' }
  catch { guestToday.value = false }
}, {immediate:true})

function continueAsGuest() {
  const date = store.puzzle?.date
  // The ref is set either way: a browser that cannot persist the choice must
  // still get past the gate for this page view.
  guestToday.value = true
  if (date) try { localStorage.setItem(guestKey(date), '1') } catch { /* nothing to do */ }
}

// account.available is null until the probe answers, and that is deliberately
// not treated as "show it": a gate that flashes up over a result the player is
// already reading is worse than one that arrives a beat late. A server with no
// sign-in configured never shows it at all.
const showGate = computed(() =>
  store.complete && account.available === true && !account.signedIn && !guestToday.value)

async function resetGame() {
  if (!confirm('Reset today’s local run and play again?')) return
  await store.resetForDevelopment()
  currentIndex.value = 0
}
</script>

<template>
  <div v-if="store.loading" class="loading">Preparing today’s board…</div>
  <div v-else-if="store.error" class="loading"><h1>Not today.</h1><p>{{store.error}}</p></div>
  <SignInGate v-else-if="showGate" @guest="continueAsGuest" />
  <ResultsView v-else-if="store.complete" />
  <StartScreen v-else-if="!store.run" :puzzle="store.puzzle!" @start="store.start" />
  <main v-else class="game game--single">
    <header class="play-header">
      <p class="eyebrow">Trivial · {{store.puzzle!.date}}</p>
      <TimerBar :remaining="store.remainingMs" :total="store.puzzle!.time_limit_seconds" />
    </header>

    <section v-if="currentQuestion" class="question-stage">
      <div class="question-meta">
        <strong>{{currentQuestion.topic_name}}</strong>
        <span>{{currentIndex + 1}} / {{questions.length}}</span>
      </div>
      <QuestionCard
        :key="currentQuestion.question_id"
        ref="card"
        :question="currentQuestion"
        :answer="store.answerMap.get(currentQuestion.question_id)"
        :disabled="store.remainingMs === 0"
        @reveal="reveal"
        @answer="answer"
      />
    </section>

    <nav class="question-nav" aria-label="Question navigation">
      <button :disabled="currentIndex === 0" @click="currentIndex--">← Back</button>
      <div class="question-dots" aria-hidden="true">
        <span v-for="(question,index) in questions" :key="question.question_id" :class="{active:index===currentIndex,answered:store.answerMap.get(question.question_id)?.outcome}" />
      </div>
      <button :disabled="currentIndex === questions.length - 1" @click="currentIndex++">Next →</button>
    </nav>

    <div class="game-actions">
      <button class="give-up" @click="store.finish">Finish early</button>
      <button v-if="isLocal" class="dev-reset" @click="resetGame">↻ Reset local game</button>
    </div>
  </main>
</template>
