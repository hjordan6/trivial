<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAdminStore } from '../stores/admin'
import { seedJSON } from '../seedjson'
import type { AdminQuestion, AdminQuestionInput } from '../types'

const props=defineProps<{
  initial?:AdminQuestion|null
  submitLabel:string
  showCancel?:boolean
  copyable?:boolean
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

// payload is what both Submit and the JSON view read, so the copied text can
// never describe a different question from the one the button would save.
const payload=computed<AdminQuestionInput>(()=>({
  topic_slug: topic.value,
  prompt: prompt.value.trim(),
  answer: answer.value.trim(),
  difficulty_rating: Number(rating.value),
  aliases: lines(aliasText.value),
  distractors: lines(distractorText.value),
  status: status.value,
}))

function submit(){ emit('submit', payload.value) }

const json=computed(()=>seedJSON(payload.value, store.topics.find(t=>t.slug===topic.value)))
const showJson=ref(false)
const copied=ref(false)
const copyNote=ref('')
let copiedTimer:ReturnType<typeof setTimeout>|undefined

async function copyJson(){
  copyNote.value=''
  try {
    if (!navigator.clipboard) throw new Error('no clipboard')
    await navigator.clipboard.writeText(json.value)
    copied.value=true
    clearTimeout(copiedTimer)
    copiedTimer=setTimeout(()=>{copied.value=false}, 2000)
  } catch {
    // The clipboard API needs a secure context and permission, and over a
    // forwarded port neither is guaranteed. Showing the JSON leaves a way to
    // select it by hand rather than a button that silently does nothing.
    showJson.value=true
    copyNote.value='Clipboard unavailable — select the JSON below instead.'
  }
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

    <div v-if="props.copyable" class="question-form__json">
      <div class="question-form__json-head">
        <button class="admin-save" type="button" @click="copyJson">
          {{copied ? 'Copied' : 'Copy as JSON'}}
        </button>
        <button class="give-up" type="button" @click="showJson=!showJson">
          {{showJson ? 'Hide JSON' : 'Show JSON'}}
        </button>
        <span class="fine">Seed format — paste it into Add questions or a seed file.</span>
      </div>
      <p v-if="copyNote" class="fine">{{copyNote}}</p>
      <textarea v-if="showJson" class="admin-import__box" rows="14" readonly :value="json"
        spellcheck="false" @focus="($event.target as HTMLTextAreaElement).select()"></textarea>
    </div>
  </form>
</template>
