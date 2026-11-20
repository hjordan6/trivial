<script setup lang="ts">
import { onMounted, ref } from 'vue';import type { Answer,Choices,Question } from '../types'
import { symbol } from '../stores/run'
const props=defineProps<{question:Question;answer?:Answer;disabled:boolean}>();const emit=defineEmits<{reveal:[number];answer:[number,'free_text'|'multiple_choice',string]}>();const open=ref(false);const text=ref('');const options=ref<string[]>([]);const struck=ref('');const busy=ref(false)
async function reveal(){busy.value=true;open.value=true;emit('reveal',props.question.question_id);busy.value=false}
function submit(){if(text.value.trim())emit('answer',props.question.question_id,'free_text',text.value.trim())}
// struck is the wrong option the player already typed, close enough to count
// as it: crossed out so they know it was read as that answer, and not offered
// again.
defineExpose({setChoices:(c:Choices)=>{options.value=c.options;struck.value=c.struck??'';open.value=true}})
onMounted(()=>{if(props.answer?.stage==='multiple_choice'&&!props.answer.outcome)reveal()})
</script>
<template><article class="card" :class="answer?.outcome&&`card--${answer.outcome}`"><header><span>{{question.difficulty}}</span><b v-if="answer?.outcome">{{symbol(answer.outcome)}}</b></header><p>{{question.prompt}}</p><template v-if="answer?.outcome"><small v-if="answer.free_text_submission">You said: {{answer.free_text_submission}}</small><strong class="answer">{{answer.canonical_answer}}</strong></template><template v-else-if="open||answer?.stage==='multiple_choice'"><div class="options"><button v-for="option in options" :key="option" :class="{'option--struck':option===struck}" :disabled="disabled||busy||option===struck" :title="option===struck?'You typed this one — not it':undefined" @click="$emit('answer',question.question_id,'multiple_choice',option)">{{option}}</button></div></template><form v-else @submit.prevent="submit"><input v-model="text" :disabled="disabled" autocomplete="off" placeholder="Your answer"><div><button :disabled="disabled||!text.trim()">Submit</button><button type="button" class="link" :disabled="disabled" @click="reveal">Show choices</button></div></form></article></template>
