<script setup lang="ts">
import { computed, onBeforeMount, ref, watch } from 'vue'
import { useAdminStore } from '../stores/admin'

const store=useAdminStore()
const chosen=ref<string[]>([])

onBeforeMount(()=>store.loadExportFields())

// The server owns which columns exist and which start ticked, so the defaults
// are applied once the list arrives rather than duplicated here.
watch(()=>store.exportFields, fields => {
  if (fields.length && !chosen.value.length) {
    chosen.value = fields.filter(f=>f.by_default).map(f=>f.name)
  }
}, {immediate:true})

function label(name:string){
  const words=name.replace(/_/g,' ')
  return words.charAt(0).toUpperCase()+words.slice(1)
}

const ordered=computed(()=>store.exportFields.filter(f=>chosen.value.includes(f.name)).map(f=>f.name))
const href=computed(()=>`/api/admin/questions/export.csv?fields=${ordered.value.join(',')}`)
const ready=computed(()=>ordered.value.length>0)
const multi=computed(()=>store.exportFields.filter(f=>f.multi_value && chosen.value.includes(f.name)))

function all(){ chosen.value=store.exportFields.map(f=>f.name) }
function none(){ chosen.value=[] }
</script>

<template>
  <section class="admin-section">
    <div class="admin-section__head">
      <h2 class="eyebrow">Export</h2>
      <span class="admin-export__picks">
        <button class="give-up" @click="all">Select all</button>
        <button class="give-up" @click="none">Clear</button>
      </span>
    </div>
    <p class="fine">
      Downloads the whole question bank as CSV — every question, ignoring the
      filters above. Tick the columns you want.
    </p>

    <div class="admin-export__fields">
      <label v-for="field in store.exportFields" :key="field.name" class="field field--check">
        <input type="checkbox" :value="field.name" v-model="chosen">
        <span>{{label(field.name)}}</span>
      </label>
    </div>

    <div class="admin-export__actions">
      <!-- A real link rather than a scripted download: the admin cookie is
           HttpOnly, and a top-level GET carries it where a blob fetch would
           need the response read back into script first. -->
      <a v-if="ready" class="primary primary--small" :href="href" download>Download CSV</a>
      <button v-else class="primary primary--small" disabled>Download CSV</button>
      <span class="fine">
        {{ready ? `${ordered.length} column${ordered.length===1?'':'s'}` : 'pick at least one column'}}
      </span>
    </div>

    <p v-if="multi.length" class="fine">
      {{multi.map(f=>label(f.name)).join(' and ')}}
      {{multi.length===1?'holds':'hold'}} several values in one cell, joined with
      <code>{{store.exportSeparator.trim()}}</code>.
    </p>
  </section>
</template>
