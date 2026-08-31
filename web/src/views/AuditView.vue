<script setup lang="ts">
import { computed, onBeforeMount, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useAdminStore } from '../stores/admin'
import { useAuditStore } from '../stores/audit'
import AdminLogin from '../components/AdminLogin.vue'

const admin=useAdminStore()
const audit=useAuditStore()

// Deleting is the one irreversible thing on this page, so it takes two clicks.
// The pending flag is cleared by every move off the card, which is why it is
// reset in navigation rather than only on cancel.
const confirming=ref(false)

const RATINGS=[1,2,3,4,5,6,7,8,9,10]
function band(rating:number){
  if (rating<=4) return 'easy'
  if (rating<=7) return 'medium'
  return 'hard'
}
// A band's first rating opens a group, which is where the easy/medium/hard
// label is drawn: the operator is picking on the 1-10 scale but thinking in
// bands, and the scale alone does not say where they change.
function startsBand(rating:number){ return rating===1||rating===5||rating===8 }

const usage=computed(()=>{
  const q=audit.current
  if (!q) return ''
  return q.used_count ? `used ${q.used_count}× · last ${q.last_used}` : 'never used'
})
// Aliases come back with the canonical answer among them; repeating it beside
// the answer itself would read as a duplicate.
const extraAliases=computed(()=>{
  const q=audit.current
  return q ? q.aliases.filter(a=>a!==q.answer) : []
})

async function go(step:()=>Promise<void>){
  confirming.value=false
  await step()
}
async function remove(){
  if (!confirming.value) { confirming.value=true; return }
  confirming.value=false
  await audit.remove()
}
async function rate(rating:number){
  confirming.value=false
  await audit.rate(rating)
}
// Retiring is reversible, so unlike delete it goes through on one click. The
// card stays put afterwards rather than advancing, so the status line and the
// way back are both still on screen.
const retired=computed(()=>audit.current?.status==='retired')
async function toggleRetired(){
  confirming.value=false
  await audit.setStatus(retired.value ? 'active' : 'retired')
}

// Arrow keys walk the queue, which is the whole point of a one-at-a-time view.
// They stand down inside form controls so the topic and difficulty selects keep
// their own arrow behaviour.
function onKey(e:KeyboardEvent){
  if (e.key!=='ArrowLeft' && e.key!=='ArrowRight') return
  const el=e.target as HTMLElement|null
  if (el && ['INPUT','SELECT','TEXTAREA'].includes(el.tagName)) return
  e.preventDefault()
  void go(e.key==='ArrowRight' ? audit.next : audit.prev)
}

watch(()=>[audit.filters.topic, audit.filters.difficulty], ()=>go(audit.restart))

// probe decides between the login form and the page. Everything the page shows
// is loaded the moment it says yes, whether that is on arrival or after a
// sign-in, so the two paths cannot drift apart.
watch(()=>admin.authed, async signedIn => {
  if (!signedIn) return
  // Signing in from here refreshes the panel's topics on the way through, so
  // they are only fetched when arriving already signed in.
  if (!admin.topics.length) await admin.loadTopics()
  await audit.load(0)
}, {immediate:true})
onBeforeMount(admin.probe)

onMounted(()=>window.addEventListener('keydown', onKey))
onBeforeUnmount(()=>window.removeEventListener('keydown', onKey))
</script>

<template>
  <div v-if="admin.authed === null" class="loading">Checking…</div>
  <AdminLogin v-else-if="!admin.authed" />
  <main v-else class="admin audit">
    <header class="admin__head">
      <p class="eyebrow">Trivial · audit</p>
      <div class="admin__head-actions">
        <a href="/admin">← Admin panel</a>
        <button class="give-up" @click="admin.logout">Sign out</button>
      </div>
    </header>

    <p v-if="audit.error" class="admin-error">{{audit.error}}</p>
    <p v-if="audit.notice" class="admin-notice">{{audit.notice}}</p>

    <div class="admin-filters audit-filters">
      <label class="field">
        <span>Topic</span>
        <select v-model="audit.filters.topic">
          <option value="">All topics</option>
          <option v-for="topic in admin.topics" :key="topic.slug" :value="topic.slug">{{topic.name}}</option>
        </select>
      </label>
      <label class="field">
        <span>Difficulty</span>
        <select v-model="audit.filters.difficulty">
          <option value="">All difficulties</option>
          <option value="easy">Easy</option>
          <option value="medium">Medium</option>
          <option value="hard">Hard</option>
        </select>
      </label>
      <p class="audit-count">
        {{audit.total ? `${audit.position} of ${audit.total}` : 'nothing matches'}}
      </p>
    </div>

    <article v-if="audit.current" class="audit-card">
      <p class="audit-card__topic">
        {{audit.current.topic_name}} · {{audit.current.difficulty}} ·
        <span :class="{'audit-card__retired':retired}">{{audit.current.status}}</span> · id {{audit.current.id}}
      </p>
      <h1 class="audit-card__prompt">{{audit.current.prompt}}</h1>

      <p class="eyebrow">Answer</p>
      <p class="audit-card__answer">{{audit.current.answer}}</p>
      <p v-if="extraAliases.length" class="fine">Also accepted: {{extraAliases.join(' · ')}}</p>

      <p class="eyebrow">Wrong options</p>
      <ul class="audit-card__options">
        <li v-for="option in audit.current.distractors" :key="option">{{option}}</li>
      </ul>

      <p class="eyebrow">Difficulty</p>
      <div class="audit-ratings" role="group" aria-label="Difficulty rating">
        <template v-for="rating in RATINGS" :key="rating">
          <span v-if="startsBand(rating)" class="audit-ratings__band">{{band(rating)}}</span>
          <button
            class="audit-rating"
            :class="{'is-current':rating===audit.current.difficulty_rating}"
            :aria-pressed="rating===audit.current.difficulty_rating"
            :disabled="audit.loading"
            @click="rate(rating)">{{rating}}</button>
        </template>
      </div>
      <p class="fine">
        Picking a rating saves it. {{usage}} — a question already on a board can be
        re-rated inside its band, but moving it to another band is refused naming the dates.
        A question that has been on a board cannot be deleted either: retire it instead, which
        keeps it off every future board without touching the ones behind it.
      </p>

      <div class="audit-actions">
        <button class="admin-save" :disabled="!audit.hasPrev || audit.loading" @click="go(audit.prev)">← Previous</button>
        <button class="admin-save" :disabled="!audit.hasNext || audit.loading" @click="go(audit.next)">Next →</button>
        <span class="audit-actions__spacer"></span>
        <button class="admin-save" :disabled="audit.loading" @click="toggleRetired">
          {{retired ? 'Make active' : 'Retire'}}
        </button>
        <button v-if="confirming" class="give-up" :disabled="audit.loading" @click="confirming=false">Cancel</button>
        <button
          class="audit-delete" :class="{'is-armed':confirming}" :disabled="audit.loading"
          @click="remove">{{confirming ? 'Delete for good' : 'Delete question'}}</button>
      </div>
    </article>

    <p v-else-if="!audit.loading" class="admin-empty">
      {{audit.total ? 'That was the last one.' : 'No questions match those filters.'}}
    </p>

    <p class="fine">← and → step through the queue.</p>
  </main>
</template>
