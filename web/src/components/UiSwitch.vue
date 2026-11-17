<script setup lang="ts">
import StudioSwitch from '@/playground/ui/UiSwitch.vue'
const model = defineModel<boolean>({ required: true })
defineProps<{ id: string }>()
</script>

<template>
  <StudioSwitch :id="id" v-model="model" class="md:ms-auto" />
</template>
