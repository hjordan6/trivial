import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import './style.css'
import App from './App.vue'
import GameView from './views/GameView.vue'
import AdminView from './views/AdminView.vue'
import AuditView from './views/AuditView.vue'
import FriendInviteView from './views/FriendInviteView.vue'

const router=createRouter({history:createWebHistory(),routes:[
  {path:'/',component:GameView},
  {path:'/admin',component:AdminView},
  {path:'/audit',component:AuditView},
  {path:'/f/:token',component:FriendInviteView},
  // Anything else is the game, which is what the server's SPA fallback serves.
  {path:'/:rest(.*)',component:GameView},
]})
createApp(App).use(createPinia()).use(router).mount('#app')
