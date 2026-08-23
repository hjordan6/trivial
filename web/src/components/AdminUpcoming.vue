<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { HORIZON_DAYS, useAdminStore } from '../stores/admin'
import type { AdminDay } from '../types'

const store=useAdminStore()

// draft holds the selects' state per date, so a half-made edit is not lost
// when the list refreshes and so Save can be disabled until something changes.
const draft=ref<Record<string,(string|null)[]>>({})

function slotsOf(day:AdminDay){ return day.slots.map(s => s.pinned ? s.topic_slug : null) }
function seed(){
  const next:Record<string,(string|null)[]> = {}
  for (const day of store.days) next[day.date] = draft.value[day.date] ?? slotsOf(day)
  draft.value = next
}
watch(()=>store.days, seed, {immediate:true, deep:false})

// After a save or a clear the server's response is the truth, so the draft has
// to be re-seeded from it. Without this a cleared date keeps showing the topic
// that was just unpinned, with Save enabled, as though nothing had happened.
function resync(date:string){
  const day=store.days.find(d=>d.date===date)
  if (day) draft.value[date]=slotsOf(day)
}
async function save(day:AdminDay){
  if (await store.saveSlots(day.date, draft.value[day.date])) resync(day.date)
}
async function clear(day:AdminDay){
  if (await store.clearSlots(day.date)) resync(day.date)
}

function dirty(day:AdminDay){
  const current=slotsOf(day), edited=draft.value[day.date] ?? current
  return edited.some((slug,i)=>slug !== current[i])
}
function pinnedCount(day:AdminDay){ return day.slots.filter(s=>s.pinned).length }
function generatedTopics(day:AdminDay){
  return day.slots.map(s=>s.topic_name).filter(Boolean).join(' · ')
}
function reason(day:AdminDay){
  if (day.has_runs) return 'already played'
  return 'today or earlier'
}
const anyUngenerated=computed(()=>store.days.some(d=>!d.generated))

function weekday(date:string){
  // Parse as UTC so a date-only string is not shifted by the local timezone.
  return new Date(date+'T00:00:00Z').toLocaleDateString(undefined,{weekday:'short',month:'short',day:'numeric',timeZone:'UTC'})
}
</script>

<template>
  <section class="admin-section">
    <div class="admin-section__head">
      <h2 class="eyebrow">Next {{HORIZON_DAYS}} days</h2>
      <button class="primary primary--small" :disabled="store.loading" @click="store.generate(HORIZON_DAYS)">
        {{store.loading?'Working…':'Generate missing days'}}
      </button>
    </div>
    <p v-if="anyUngenerated" class="fine">Dates with no board yet are generated with whatever is pinned to them, so you can pin first and generate after.</p>

    <div class="admin-days">
      <div v-for="day in store.days" :key="day.date" class="admin-day" :class="{'admin-day--locked':!day.editable}">
        <div class="admin-day__when">
          <b>{{weekday(day.date)}}</b>
          <small v-if="!day.generated">no board yet</small>
          <small v-else-if="pinnedCount(day)">{{pinnedCount(day)}}/3 pinned</small>
          <small v-else>automatic</small>
        </div>

        <div v-if="day.editable" class="admin-day__slots">
          <label v-for="(slot,index) in day.slots" :key="slot.position" class="field field--slot">
            <span>Slot {{index+1}}</span>
            <select v-model="draft[day.date][index]" :class="{'is-pinned':draft[day.date][index]}">
              <option :value="null">Automatic</option>
              <option v-for="topic in store.topics" :key="topic.slug" :value="topic.slug">{{topic.name}}</option>
            </select>
          </label>
        </div>
        <p v-else class="admin-day__locked">
          {{generatedTopics(day) || 'no board'}}
          <small>{{reason(day)}} — cannot change</small>
        </p>

        <div v-if="day.editable" class="admin-day__actions">
          <button class="admin-save" :disabled="store.loading || !dirty(day)"
            @click="save(day)">Save</button>
          <button v-if="pinnedCount(day)" class="dev-reset" :disabled="store.loading"
            @click="clear(day)">Reset to automatic</button>
        </div>

        <p v-if="day.editable && day.generated" class="admin-day__result">{{generatedTopics(day)}}</p>
      </div>
    </div>
  </section>
</template>
