<script setup lang="ts">
import { ref } from 'vue'
import { useAdminStore } from '../stores/admin'
import QuestionForm from './QuestionForm.vue'
import type { AdminQuestionInput } from '../types'

const store=useAdminStore()
const form=ref<InstanceType<typeof QuestionForm>|null>(null)

async function submit(input:AdminQuestionInput){
  // Clearing only on success leaves a rejected question on screen to be fixed
  // rather than retyped.
  if (await store.createQuestion(input)) form.value?.reset()
}
</script>

<template>
  <section class="admin-section">
    <h2 class="eyebrow">Write a question</h2>
    <p class="fine">
      Added straight to the library. Set it Active and it is eligible for the
      next board you generate; save it as a Draft to keep it out of selection
      until it is ready.
    </p>
    <QuestionForm ref="form" submit-label="Add question" @submit="submit" />
  </section>
</template>
