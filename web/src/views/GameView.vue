<script setup lang="ts">
import { computed, onBeforeMount, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRunStore } from '../stores/run'
import StartScreen from '../components/StartScreen.vue'
import TimerBar from '../components/TimerBar.vue'
import QuestionCard from '../components/QuestionCard.vue'
import ResultsView from '../components/ResultsView.vue'

const store = useRunStore()
const currentIndex = ref(0)
const card = ref<InstanceType<typeof QuestionCard>>()
const isLocal = ['localhost', '127.0.0.1', '::1'].includes(location.hostname)
const questions = computed(() => store.puzzle?.questions ?? [])
const currentQuestion = computed(() => questions.value[currentIndex.value])

onBeforeMount(store.load)
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
async function resetGame() {
  if (!confirm('Reset today’s local run and play again?')) return
  await store.resetForDevelopment()
  currentIndex.value = 0
}
</script>

<template>
  <div v-if="store.loading" class="loading">Preparing today’s board…</div>
  <div v-else-if="store.error" class="loading"><h1>Not today.</h1><p>{{store.error}}</p></div>
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
