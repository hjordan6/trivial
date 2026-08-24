<script setup lang="ts">
import { computed, onBeforeMount, ref, watch } from 'vue'
import { useAdminStore } from '../stores/admin'
import type { AdminBoardEntry } from '../types'

const store=useAdminStore()
const date=ref('')

// Revealed answers are keyed by question id and cleared whenever the date
// changes, so stepping through days never carries a reveal onto the next board.
const revealed=ref(new Set<number>())
function reveal(entry:AdminBoardEntry){
  if (revealed.value.has(entry.id)) revealed.value.delete(entry.id)
  else revealed.value.add(entry.id)
}

// The answer sits among the distractors in alphabetical order, so its position
// gives nothing away while it is still hidden.
function choices(entry:AdminBoardEntry){
  return [entry.answer, ...entry.distractors].sort((a,b)=>a.localeCompare(b))
}

// The board's first day is today as the server reckons it, which is the date
// the operator means by "today" -- the browser's clock may be in another zone.
watch(()=>store.days, days => { if (!date.value && days.length) date.value = days[0].date }, {immediate:true})
watch(date, value => { revealed.value.clear(); store.loadBoard(value) })

function step(days:number){
  if (!date.value) return
  const d=new Date(date.value+'T00:00:00Z')
  d.setUTCDate(d.getUTCDate()+days)
  date.value=d.toISOString().slice(0,10)
}

// Entries arrive in board order, so grouping preserves it without sorting.
const byTopic=computed(()=>{
  const groups:{position:number; name:string; entries:AdminBoardEntry[]}[]=[]
  for (const entry of store.board) {
    let group=groups.find(g=>g.position===entry.topic_position)
    if (!group) { group={position:entry.topic_position, name:entry.topic_name, entries:[]}; groups.push(group) }
    group.entries.push(entry)
  }
  return groups
})

function weekday(value:string){
  return new Date(value+'T00:00:00Z').toLocaleDateString(undefined,
    {weekday:'long', month:'long', day:'numeric', timeZone:'UTC'})
}

onBeforeMount(()=>{ if (date.value) store.loadBoard(date.value) })
</script>

<template>
  <section class="admin-section">
    <div class="admin-section__head">
      <h2 class="eyebrow">Board for a day</h2>
      <span class="admin-count">
        <span v-if="store.boardHasRuns">already played</span>
        <span v-else-if="store.boardGenerated">not played yet</span>
      </span>
    </div>
    <p class="fine">The nine questions scheduled for one date. Answers stay hidden until you click them.</p>

    <div class="admin-board__pick">
      <button class="admin-save" :disabled="store.boardLoading" @click="step(-1)">← Previous day</button>
      <label class="field field--inline">
        <span>Date</span>
        <input v-model="date" type="date">
      </label>
      <button class="admin-save" :disabled="store.boardLoading" @click="step(1)">Next day →</button>
    </div>

    <p v-if="date" class="admin-board__when">{{weekday(date)}}</p>

    <p v-if="!store.boardGenerated && !store.boardLoading" class="admin-empty">
      No board has been generated for this date.
    </p>

    <div v-else class="admin-board">
      <div v-for="group in byTopic" :key="group.position" class="admin-board__topic">
        <p class="eyebrow">Slot {{group.position+1}} · {{group.name}}</p>
        <div v-for="entry in group.entries" :key="entry.id" class="admin-board__q">
          <span class="admin-board__slot">{{entry.slot}} · {{entry.difficulty_rating}}</span>
          <div class="admin-board__body">
            <p class="admin-board__prompt">{{entry.prompt}}</p>
            <ul v-if="revealed.has(entry.id)" class="admin-board__choices">
              <li v-for="option in choices(entry)" :key="option"
                :class="{'is-correct':option===entry.answer}">{{option}}</li>
            </ul>
          </div>
          <button class="admin-q__answer" :class="{'is-hidden':!revealed.has(entry.id)}"
            :title="revealed.has(entry.id) ? 'Hide the answer' : 'Show the answer'"
            @click="reveal(entry)">
            {{revealed.has(entry.id) ? entry.answer : '••••••'}}
          </button>
        </div>
      </div>
    </div>
  </section>
</template>
