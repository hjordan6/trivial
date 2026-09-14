<script setup lang="ts">
import { computed, onBeforeMount, ref } from 'vue'
import { useLeaderboardStore } from '../stores/leaderboard'
import { useRunStore, symbol, topicEmoji } from '../stores/run'
import { useAccountStore } from '../stores/account'
import type { FriendToday, Question } from '../types'
import SignIn from '../components/SignIn.vue'
import SendFriendRequest from '../components/SendFriendRequest.vue'

const board=useLeaderboardStore()
const run=useRunStore()
const account=useAccountStore()

// The run store is loaded for the puzzle, not for the run: orderedRows() is
// what turns nine question ids into three topic rows, and the side-by-side has
// to lay both boards out the same way the results screen does. /api/runs/current
// returns the puzzle whether or not this browser has started it.
onBeforeMount(()=>{board.load();run.load();account.probe()})

const formatter=new Intl.DateTimeFormat(undefined,{weekday:'long',month:'long',day:'numeric'})
function formatDay(iso:string):string {
  if(!iso) return ''
  const [year,month,day]=iso.split('-').map(Number)
  return formatter.format(new Date(year,month-1,day))
}
function cell(person:FriendToday|undefined, questionID:number):string {
  return symbol(board.outcomes(person).get(questionID))
}

// The right answer, read off the viewer's OWN run rather than the friend's.
//
// That is the only source that discloses nothing new. The server fills
// canonical_answer in on a run whose question is resolved -- or on any question
// once the run is finished -- and withholds it otherwise, so this shows exactly
// what the viewer's own results screen already shows them. Taking it from the
// friend's board instead would let somebody who has not played yet read today's
// answers off a friend who finished early, which is the reason a friend's
// answer carries an outcome and nothing else.
function answerOf(questionID:number):string {
  return run.answerMap.get(questionID)?.canonical_answer ?? ''
}
// True once the viewer's own run can supply the answers. Before that the
// comparison still renders -- the grids are not a spoiler -- but the answer
// column would be blank, so the page says why instead.
const answersReady=computed(() => Boolean(run.complete))

// Tapping an answer opens the question behind it.
//
// A native <dialog> rather than a hand-rolled overlay: showModal() gives
// dismissal on Escape, a focus trap, inertness for the page behind it and a
// ::backdrop to style, none of which would come free from a positioned div.
// Every one of those is part of "easily dismissable".
const detail=ref<HTMLDialogElement>()
const openQuestion=ref<Question|null>(null)

function openDetail(question:Question) {
  openQuestion.value=question
  detail.value?.showModal()
}
function closeDetail() { detail.value?.close() }
// Clicking the backdrop closes it. A click on the backdrop has the dialog
// itself as its target, so .self is what separates it from a click on the card
// inside -- which is why the content sits in its own element.
function onDialogClick(event:MouseEvent) {
  if (event.target === detail.value) closeDetail()
}
</script>

<template>
  <main class="board">
    <p class="eyebrow">{{formatDay(board.date)}}</p>
    <h1>Friends’ Scores</h1>

    <!-- Two boards, one question. Today changes daily; all-time is the record
         behind it. -->
    <nav v-if="!board.signedOut" class="tabs">
      <button type="button" :class="{'tabs__on':board.tab==='today'}" @click="board.showTab('today')">Today</button>
      <button type="button" :class="{'tabs__on':board.tab==='all-time'}" @click="board.showTab('all-time')">All time</button>
    </nav>

    <template v-if="board.tab==='all-time'">
      <p v-if="board.allTimeLoading" class="board-note">Adding up every day…</p>
      <p v-else-if="board.allTimeError" class="board-note">{{board.allTimeError}}</p>
      <template v-else-if="board.allTime">
        <!-- Said before the numbers, not after: an unqualified viewer needs to
             know why they are absent from the list they are looking at. -->
        <p v-if="!board.allTime.qualified" class="board-note">
          You’ve played {{board.allTime.days_played}}
          {{board.allTime.days_played===1?'day':'days'}}. Play
          {{board.allTime.minimum_days-board.allTime.days_played}} more to join the rankings.
        </p>

        <p class="eyebrow alltime__eyebrow">Average points per day</p>
        <ol class="board-list">
          <li v-for="e in board.allTime.friends" :key="e.user_id" class="row" :class="{'row--you':e.you}">
            <div class="row__grid">
              <span class="board-row__name">
                <b class="alltime__rank">{{e.rank}}</b>{{e.nickname}}
                <small v-if="e.you">you</small>
              </span>
              <span class="board-row__score">{{e.average_points}}</span>
              <span class="board-row__points">{{e.days_played}} days</span>
            </div>
          </li>
        </ol>
        <p v-if="!board.allTime.friends.length" class="board-note">
          Nobody here has played {{board.allTime.minimum_days}} days yet.
        </p>

        <!-- Everyone: a position and a field size. Deliberately no names -- see
             GlobalStanding in the Go. -->
        <section class="global">
          <p class="eyebrow">Everyone</p>
          <p v-if="board.allTime.global.ranked" class="global__line">
            <b>{{board.allTime.global.rank}}</b> of {{board.allTime.global.of}}
            <span>· best on record {{board.allTime.global.best_average}}</span>
          </p>
          <p v-else class="board-note">Not ranked yet.</p>
        </section>

        <p class="eyebrow alltime__eyebrow">By category</p>
        <section v-for="topic in board.allTime.topics" :key="topic.slug" class="topic-block">
          <h2 class="topic-block__head">
            <span>{{topicEmoji(topic.slug,topic.name)}} {{topic.name}}</span>
            <small>{{topic.asked}} asked</small>
          </h2>
          <ol v-if="topic.ranked" class="topic-block__list">
            <li v-for="e in topic.friends" :key="e.user_id" :class="{'topic-block__you':e.you}">
              <b class="alltime__rank">{{e.rank}}</b>
              <span>{{e.you?'You':e.nickname}}</span>
              <span class="topic-block__avg">{{e.average_points}}</span>
            </li>
          </ol>
          <p v-else class="topic-block__thin">
            {{topic.asked}} of {{board.allTime.minimum_questions}} questions — not ranked yet.
          </p>
          <p v-if="topic.ranked&&topic.global.ranked" class="topic-block__global">
            {{topic.global.rank}} of {{topic.global.of}} overall
          </p>
        </section>
        <p v-if="!board.allTime.topics.length" class="board-note">Play a day and your categories show up here.</p>
      </template>
    </template>

    <template v-else>
    <p v-if="board.loading" class="board-note">Reading the scores…</p>
    <p v-else-if="board.error" class="board-note">{{board.error}}</p>

    <!-- Signed out is not an error: there is nothing to show, and the only
         useful thing the page can do is offer the way in. -->
    <template v-else-if="board.signedOut">
      <p class="board-note">Sign in to see how your friends did today.</p>
      <SignIn />
    </template>

    <!-- One list, ranked by score, with the viewer in it rather than pinned
         above it: a friend who beat you today belongs above you. -->
    <template v-else-if="!board.openFriend">
      <ol class="board-list">
        <li v-for="person in board.friends" :key="person.user_id" class="row" :class="{'row--you':person.you}">
          <!-- Only somebody else who finished has a board to put beside yours;
               your own row is where you already are. -->
          <button v-if="person.played&&!person.you" class="row__grid row__open" @click="board.open(person.user_id)">
            <span class="board-row__name">{{person.nickname}}</span>
            <span class="board-row__score">{{person.correct}}/9</span>
            <span class="board-row__points">{{person.points}} pts</span>
            <span class="board-row__chevron" aria-hidden="true">→</span>
          </button>
          <div v-else class="row__grid">
            <span class="board-row__name">{{person.nickname}}<small v-if="person.you">you</small></span>
            <template v-if="person.played">
              <span class="board-row__score">{{person.correct}}/9</span>
              <span class="board-row__points">{{person.points}} pts</span>
            </template>
            <span v-else class="board-row__pending">Not played yet</span>
          </div>
        </li>
      </ol>

      <p v-if="!board.hasFriends" class="board-empty">You don’t have any trivial friends yet!</p>

      <RouterLink class="history-link" to="/">← Back to today’s game</RouterLink>
    </template>

    <!-- The comparison: the friend's board above the viewer's, both drawn with
         the same three-column grid the results screen uses, and the right
         answer under each column. Everything inside one grid per topic, so the
         two rows of glyphs and the answer beneath them stay in line. -->
    <template v-else>
      <section class="versus">
        <header class="versus__head">
          <div>
            <b>{{board.selected?.nickname}}</b>
            <small>{{board.selected?.correct}}/9 · {{board.selected?.points}} pts</small>
          </div>
          <div>
            <b>You</b>
            <small>{{board.you?.played?`${board.you.correct}/9 · ${board.you.points} pts`:'Not played yet'}}</small>
          </div>
        </header>

        <div v-for="(row,position) in run.orderedRows()" :key="position" class="versus__row">
          <h2 class="versus__topic">{{row[0]?.topic_name}}</h2>
          <div class="versus__board">
            <p class="versus__who">{{board.selected?.nickname}}</p>
            <b v-for="q in row" :key="`t${q.question_id}`" class="versus__mark">{{cell(board.selected,q.question_id)}}</b>
            <p class="versus__who">You</p>
            <b v-for="q in row" :key="`y${q.question_id}`" class="versus__mark">{{cell(board.you,q.question_id)}}</b>
            <template v-if="answersReady">
              <p class="versus__who">Answer</p>
              <!-- A button, not a span: this is the only way to reach the
                   question text, so it has to be reachable by keyboard too. -->
              <button
                v-for="q in row" :key="`a${q.question_id}`" type="button" class="versus__answer"
                :aria-label="`Show the question for ${answerOf(q.question_id)}`"
                @click="openDetail(q)">{{answerOf(q.question_id)}}</button>
            </template>
          </div>
        </div>

        <!-- Shown once, under the whole board, rather than as three empty
             answer rows that look like missing data. -->
        <p v-if="!answersReady" class="board-note versus__locked">
          Finish today’s board and the answers show up here.
        </p>
      </section>

      <button class="dev-reset" @click="board.close()">← All results</button>
    </template>

    </template>

    <dialog ref="detail" class="qdialog" @click="onDialogClick" @close="openQuestion=null">
      <article v-if="openQuestion" class="qdialog__card">
        <p class="qdialog__meta">{{openQuestion.topic_name}} · {{openQuestion.difficulty}}</p>
        <p class="qdialog__prompt">{{openQuestion.prompt}}</p>
        <p class="qdialog__answer">{{answerOf(openQuestion.question_id)}}</p>
        <div class="qdialog__marks">
          <span><b>{{cell(board.selected,openQuestion.question_id)}}</b> {{board.selected?.nickname}}</span>
          <span><b>{{cell(board.you,openQuestion.question_id)}}</b> You</span>
        </div>
        <button class="primary primary--small" type="button" autofocus @click="closeDetail">Close</button>
      </article>
    </dialog>

    <SendFriendRequest v-if="!board.signedOut" link />

    <!-- The account line, and with it the way out. It sits outside the branches
         above because this is the page where wanting to look as somebody else
         is the ordinary case, and the sign-out button lives inside this panel. -->
    <SignIn v-if="account.signedIn" />
  </main>
</template>
