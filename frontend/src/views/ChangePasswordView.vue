<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NForm, NFormItem, NInput, useMessage } from 'naive-ui'
import { api, errorText } from '../api'
import { useAuth } from '../stores/auth'
import { platformName } from '../stores/brand'

const auth = useAuth(), router = useRouter(), message = useMessage()
const currentPassword = ref(''), newPassword = ref(''), confirmation = ref(''), saving = ref(false)
async function submit() {
  if (saving.value) return
  const bytes = new TextEncoder().encode(newPassword.value).length
  if (!currentPassword.value || bytes < 6 || bytes > 72) { message.warning('请填写当前密码，新密码需为 6–72 字节'); return }
  if (newPassword.value === currentPassword.value) { message.warning('新密码不能与当前密码相同'); return }
  if (newPassword.value !== confirmation.value) { message.warning('两次输入的新密码不一致'); return }
  saving.value = true
  try {
    await api.put('/auth/password', {old_password:currentPassword.value, new_password:newPassword.value})
    currentPassword.value = ''; newPassword.value = ''; confirmation.value = ''
    auth.logout()
    message.success('密码已修改，请使用新密码登录')
    await router.replace('/login')
  } catch (error) { message.error(errorText(error)) }
  finally { saving.value = false }
}
async function logout() {
  if (saving.value) return
  saving.value = true
  try { await api.post('/auth/logout'); auth.logout(); await router.replace('/login') }
  catch (error) { message.error(errorText(error)) }
  finally { saving.value = false }
}
</script>
<template>
  <main class="password-change-screen">
    <section class="password-change-card">
      <div class="eyebrow dark">{{ platformName }}</div>
      <h1>修改初始密码</h1>
      <p>当前账号：{{ auth.user?.username }}</p>
      <NAlert type="info" :bordered="false">管理员要求首次登录修改密码。完成后使用新密码重新登录，即可访问已授权的功能。</NAlert>
      <NForm label-placement="top" :disabled="saving" @submit.prevent="submit">
        <NFormItem label="当前密码" required><NInput v-model:value="currentPassword" type="password" show-password-on="click" placeholder="管理员提供的初始密码" :input-props="{autocomplete:'current-password'}" /></NFormItem>
        <NFormItem label="新密码" required><NInput v-model:value="newPassword" type="password" show-password-on="click" placeholder="6–72 字节，且不同于当前密码" :input-props="{autocomplete:'new-password'}" /></NFormItem>
        <NFormItem label="确认新密码" required><NInput v-model:value="confirmation" type="password" show-password-on="click" placeholder="再次输入新密码" :input-props="{autocomplete:'new-password'}" @keyup.enter="submit" /></NFormItem>
        <div class="password-change-actions"><NButton :disabled="saving" @click="logout">退出登录</NButton><NButton type="primary" attr-type="submit" :loading="saving">保存新密码</NButton></div>
      </NForm>
    </section>
  </main>
</template>
<style scoped>
.password-change-screen { min-height:100dvh; display:grid; place-items:center; padding:24px 16px; background:var(--color-bg); }
.password-change-card { width:min(460px,100%); box-sizing:border-box; padding:32px; background:var(--color-surface); border:1px solid var(--color-border); border-radius:var(--radius-sm); }
.password-change-card h1 { font-size:24px; margin:16px 0 8px; }
.password-change-card p { color:var(--color-text-secondary); margin-bottom:20px; }
.password-change-card form { margin-top:24px; }
.password-change-actions { display:flex; justify-content:space-between; gap:16px; }
@media(max-width:480px) { .password-change-card { padding:24px 20px; } }
</style>
