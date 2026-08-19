<script setup lang="ts">
import { computed } from 'vue'
const props=defineProps<{remaining:number,total:number}>()
const label=computed(()=>`${Math.floor(props.remaining/60000)}:${String(Math.ceil(props.remaining/1000)%60).padStart(2,'0')}`)
const width=computed(()=>`${Math.min(100,props.remaining/(props.total*10))}%`)
</script>
<template><div class="timer" role="timer"><div class="timer__track"><span :style="{width}" /></div><strong>{{label}}</strong></div></template>
