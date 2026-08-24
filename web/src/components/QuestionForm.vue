<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAdminStore } from '../stores/admin'
import type { AdminQuestion, AdminQuestionInput } from '../types'

const props=defineProps<{
  initial?:AdminQuestion|null
  submitLabel:string
  showCancel?:boolean
}>()
const emit=defineEmits<{submit:[AdminQuestionInput]; cancel:[]}>()

const store=useAdminStore()

const topic=ref('')
const rating=ref(5)
const prompt=ref('')
const answer=ref('')
const aliasText=ref('')
const distractorText=ref('')
const status=ref('active')

function lines(value:string){ return value.split('\n').map(v=>v.trim()).filter(Boolean) }

// Validation prepends the canonical answer to the stored aliases, so it comes
// back as one of them. Showing it in the "also accepted" box would read as a
// duplicate of the answer field, and re-submitting it changes nothing.
function extraAliases(q:AdminQuestion){ return q.aliases.filter(a=>a!==q.answer) }

function reset(){
  const q=props.initial
  topic.value = q?.topic_slug ?? store.topics[0]?.slug ?? ''
  rating.value = q?.difficulty_rating ?? 5
  prompt.value = q?.prompt ?? ''
  answer.value = q?.answer ?? ''
  aliasText.value = q ? extraAliases(q).join('\n') : ''
  distractorText.value = q ? q.distractors.join('\n') : ''
  status.value = q?.status ?? 'active'
}
watch(()=>props.initial, reset, {immediate:true})
// A newly signed-in panel has no topics until the first refresh lands, so the
// default topic is filled in when the list arrives.
watch(()=>store.topics, ()=>{ if (!topic.value) topic.value=store.topics[0]?.slug ?? '' })

const band=computed(()=>{
  if (rating.value>=1 && rating.value<=4) return 'easy'
  if (rating.value>=5 && rating.value<=7) return 'medium'
  if (rating.value>=8 && rating.value<=10) return 'hard'
  return '—'
})

// A light echo of the server's rule, to catch the obvious case before a round
// trip. The server stays the authority: it compares normalized forms, which is
// stricter than this case-insensitive check.
const distinctDistractors=computed(()=>new Set(lines(distractorText.value).map(d=>d.toLowerCase())).size)
const ready=computed(()=>
  !!topic.value && !!prompt.value.trim() && !!answer.value.trim() &&
  rating.value>=1 && rating.value<=10 && distinctDistractors.value>=3)

function submit(){
  emit('submit', {
    topic_slug: topic.value,
    prompt: prompt.value.trim(),
    answer: answer.value.trim(),
    difficulty_rating: Number(rating.value),
    aliases: lines(aliasText.value),
    distractors: lines(distractorText.value),
    status: status.value,
  })
}

defineExpose({reset})
</script>

<template>
  <form class="question-form" @submit.prevent="submit">
    <div class="question-form__row">
      <label class="field">
        <span>Topic</span>
        <select v-model="topic">
          <option v-for="t in store.topics" :key="t.slug" :value="t.slug">{{t.name}}</option>
        </select>
      </label>
      <label class="field">
        <span>Difficulty 1–10 ({{band}})</span>
        <input v-model.number="rating" type="number" min="1" max="10">
      </label>
      <label class="field">
        <span>Status</span>
        <select v-model="status">
          <option value="active">Active</option>
          <option value="draft">Draft</option>
          <option value="retired">Retired</option>
        </select>
      </label>
    </div>

    <label class="field">
      <span>Question</span>
      <textarea v-model="prompt" rows="3" placeholder="What is the capital city of Italy?"></textarea>
    </label>

    <label class="field">
      <span>Answer</span>
      <input v-model="answer" type="text" placeholder="Rome">
    </label>

    <div class="question-form__row question-form__row--pair">
      <label class="field">
        <span>Also accepted, one per line</span>
        <textarea v-model="aliasText" rows="4" placeholder="Roma"></textarea>
      </label>
      <label class="field">
        <span>Wrong options, one per line (3+)</span>
        <textarea v-model="distractorText" rows="4" placeholder="Milan&#10;Naples&#10;Turin"></textarea>
      </label>
    </div>

    <div class="question-form__actions">
      <button class="primary primary--small" type="submit" :disabled="!ready || store.questionsLoading">
        {{store.questionsLoading ? 'Saving…' : props.submitLabel}}
      </button>
      <button v-if="props.showCancel" class="give-up" type="button" @click="emit('cancel')">Cancel</button>
      <span v-if="distinctDistractors<3" class="fine">
        {{3-distinctDistractors}} more wrong option{{3-distinctDistractors===1?'':'s'}} needed
      </span>
      <span v-else-if="!ready" class="fine">topic, question and answer are all required</span>
    </div>
  </form>
</template>
