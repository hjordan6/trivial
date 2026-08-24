<script setup lang="ts">
import { computed, onBeforeMount, ref, watch } from 'vue'
import { QUESTION_PAGE, useAdminStore } from '../stores/admin'
import type { AdminQuestion } from '../types'

const store=useAdminStore()

// Both sets are keyed by question id rather than row index, so paging or
// refiltering cannot carry a reveal over to whichever question lands in that
// row next. Answers start hidden on every load, which is the point of them.
const expanded=ref(new Set<number>())
const revealed=ref(new Set<number>())

function toggle(q:AdminQuestion){
  if (expanded.value.has(q.id)) expanded.value.delete(q.id)
  else expanded.value.add(q.id)
}
function reveal(q:AdminQuestion){
  if (revealed.value.has(q.id)) revealed.value.delete(q.id)
  else revealed.value.add(q.id)
}

// The answer sits among the distractors in alphabetical order, so its position
// never gives it away while it is still redacted.
function choices(q:AdminQuestion){
  return [q.answer, ...q.distractors].sort((a,b)=>a.localeCompare(b))
}
function isAnswer(q:AdminQuestion, option:string){ return option===q.answer }

// Aliases are extra spellings the grader accepts, so they give the answer away
// just as surely as the answer does and stay hidden with it.
function usage(q:AdminQuestion){
  if (!q.used_count) return 'never used'
  return `used ${q.used_count}× · last ${q.last_used}`
}

let timer:ReturnType<typeof setTimeout>|undefined
watch(()=>store.filters.search, ()=>{
  clearTimeout(timer)
  timer=setTimeout(()=>store.loadQuestions(0), 250)
})
watch(()=>[store.filters.topic, store.filters.difficulty], ()=>store.loadQuestions(0))

const from=computed(()=>store.questionTotal ? store.questionOffset+1 : 0)
const to=computed(()=>Math.min(store.questionOffset+QUESTION_PAGE, store.questionTotal))
const hasPrev=computed(()=>store.questionOffset>0)
const hasNext=computed(()=>to.value<store.questionTotal)

onBeforeMount(()=>store.loadQuestions(0))
</script>

<template>
  <section class="admin-section">
    <div class="admin-section__head">
      <h2 class="eyebrow">Question library</h2>
      <span class="admin-count">
        {{store.questionTotal ? `${from}–${to} of ${store.questionTotal}` : 'nothing matches'}}
      </span>
    </div>
    <p class="fine">Click a row for the full question and its options. Answers stay hidden until you click them.</p>

    <div class="admin-filters">
      <label class="field">
        <span>Topic</span>
        <select v-model="store.filters.topic">
          <option value="">All topics</option>
          <option v-for="topic in store.topics" :key="topic.slug" :value="topic.slug">{{topic.name}}</option>
        </select>
      </label>
      <label class="field">
        <span>Difficulty</span>
        <select v-model="store.filters.difficulty">
          <option value="">All difficulties</option>
          <option value="easy">Easy</option>
          <option value="medium">Medium</option>
          <option value="hard">Hard</option>
        </select>
      </label>
      <label class="field">
        <span>Search</span>
        <input v-model="store.filters.search" type="search" placeholder="prompt or answer">
      </label>
    </div>

    <div class="admin-questions">
      <div
        v-for="q in store.questions" :key="q.id"
        class="admin-q" :class="{'admin-q--open':expanded.has(q.id)}">
        <div
          class="admin-q__row" role="button" tabindex="0" :aria-expanded="expanded.has(q.id)"
          @click="toggle(q)" @keydown.enter.prevent="toggle(q)" @keydown.space.prevent="toggle(q)">
          <span class="admin-q__topic">{{q.topic_name}}</span>
          <span class="admin-q__prompt" :class="{'admin-q__prompt--full':expanded.has(q.id)}">{{q.prompt}}</span>
          <span class="admin-q__difficulty">{{q.difficulty}} · {{q.difficulty_rating}}</span>
          <button
            class="admin-q__answer" :class="{'is-hidden':!revealed.has(q.id)}"
            :title="revealed.has(q.id) ? 'Hide the answer' : 'Show the answer'"
            @click.stop="reveal(q)" @keydown.enter.stop @keydown.space.stop>
            {{revealed.has(q.id) ? q.answer : '••••••'}}
          </button>
        </div>

        <div v-if="expanded.has(q.id)" class="admin-q__detail">
          <div class="admin-q__options">
            <p class="eyebrow">Answer choices</p>
            <ul>
              <li
                v-for="option in choices(q)" :key="option"
                :class="{'is-correct':revealed.has(q.id) && isAnswer(q, option)}">
                {{option}}
              </li>
            </ul>
          </div>
          <div class="admin-q__meta">
            <p class="eyebrow">Also accepted</p>
            <p v-if="revealed.has(q.id)">{{q.aliases.join(' · ')}}</p>
            <p v-else class="admin-q__masked">hidden with the answer</p>
            <p class="eyebrow">Usage</p>
            <p>{{usage(q)}}</p>
            <p class="fine">{{q.status}} · id {{q.id}}</p>
          </div>
        </div>
      </div>

      <p v-if="!store.questions.length && !store.questionsLoading" class="admin-empty">
        No questions match those filters.
      </p>
    </div>

    <div v-if="hasPrev || hasNext" class="admin-pager">
      <button class="admin-save" :disabled="!hasPrev || store.questionsLoading"
        @click="store.loadQuestions(Math.max(0, store.questionOffset-QUESTION_PAGE))">← Previous</button>
      <button class="admin-save" :disabled="!hasNext || store.questionsLoading"
        @click="store.loadQuestions(store.questionOffset+QUESTION_PAGE)">Next →</button>
    </div>
  </section>
</template>
