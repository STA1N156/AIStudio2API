<script setup lang="ts">
import { ref } from 'vue'
import { api, type AdminSession } from '@/api'
import { useI18n } from '@/i18n'
import ThemeToggle from './ThemeToggle.vue'

const emit = defineEmits<{ authenticated: [session: AdminSession] }>()
const { t, errorText } = useI18n()
const username = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')

// login 提交管理员凭据并清理密码输入
async function login(): Promise<void> {
  busy.value = true
  error.value = ''
  try {
    emit('authenticated', await api.login(username.value, password.value))
  } catch (cause) {
    error.value = errorText(cause)
  } finally {
    password.value = ''
    busy.value = false
  }
}
</script>

<template>
  <main class="page-panel flex h-full items-center justify-center overflow-auto bg-canvas p-5">
    <form
      class="w-full max-w-sm space-y-6 rounded-2xl border border-line bg-panel p-7 shadow-xl shadow-black/10"
      @submit.prevent="login"
    >
      <div class="flex items-center justify-between gap-2">
        <h1 class="text-xl font-semibold text-white">AI Studio</h1>
        <ThemeToggle />
      </div>
      <label class="block">
        <span class="mb-2 block text-sm text-gray-300">{{ t('auth.username') }}</span>
        <input
          v-model.trim="username"
          name="username"
          autocomplete="username"
          autofocus
          required
          class="ui-input"
        />
      </label>
      <label class="block">
        <span class="mb-2 block text-sm text-gray-300">{{ t('auth.password') }}</span>
        <input
          v-model="password"
          name="password"
          type="password"
          autocomplete="current-password"
          required
          class="ui-input"
        />
      </label>
      <p v-if="error" role="alert" class="text-sm text-red-300">{{ error }}</p>
      <button :disabled="busy" type="submit" class="ui-button ui-button-primary w-full">
        {{ busy ? t('common.loading') : t('auth.login') }}
      </button>
    </form>
  </main>
</template>
