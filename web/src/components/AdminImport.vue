<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAdminStore } from '../stores/admin'

const store=useAdminStore()
const text=ref('')
const open=ref(false)

const example=`{
  "topics": [
    {
      "slug": "world-geography",
      "name": "World Geography",
      "weight": 1,
      "questions": [
        {
          "external_id": "world-geography-1",
          "difficulty": 3,
          "prompt": "What is the capital city of Italy?",
          "answer": "Rome",
          "aliases": [],
          "distractors": ["Milan", "Naples", "Turin"]
        }
      ]
    }
  ]
}`

// The server refuses a paste it cannot parse, so the button only guards
// against submitting nothing at all.
const empty=computed(()=>!text.value.trim())

async function submit(){
  if (await store.importQuestions(text.value)) text.value=''
}
</script>

<template>
  <section class="admin-section">
    <div class="admin-section__head">
      <h2 class="eyebrow">Add questions</h2>
      <button class="give-up" @click="open=!open">{{open?'Hide the format':'What format?'}}</button>
    </div>
    <p class="fine">
      Paste the same JSON a seed file holds. Questions arrive active, so they are
      eligible for the next board you generate.
    </p>

    <div v-if="open" class="admin-import__help">
      <p class="fine">
        Three shapes are accepted: <code>{"topics": […]}</code> as below, a flat
        <code>{"questions": […]}</code> list, or a bare <code>[…]</code> array using
        <code>question</code>/<code>category</code>/<code>acceptedAnswers</code>/<code>multipleChoiceOptions</code>.
      </p>
      <p class="fine">
        <code>difficulty</code> is 1–10 (1–4 easy, 5–7 medium, 8–10 hard); the old
        <code>"easy"</code>/<code>"medium"</code>/<code>"hard"</code> strings still work.
        Each question needs at least three distractors that are not also accepted answers,
        or it could never appear on a board. Re-pasting the same
        <code>external_id</code> updates that question rather than adding a duplicate.
      </p>
      <pre>{{example}}</pre>
    </div>

    <textarea
      v-model="text" class="admin-import__box" rows="10" spellcheck="false"
      placeholder='{"topics": [ … ]}'></textarea>

    <div class="admin-import__actions">
      <button class="primary primary--small" :disabled="empty || store.questionsLoading" @click="submit">
        {{store.questionsLoading?'Importing…':'Import'}}
      </button>
      <button v-if="text" class="dev-reset" :disabled="store.questionsLoading" @click="text=''">Clear</button>
    </div>
  </section>
</template>
