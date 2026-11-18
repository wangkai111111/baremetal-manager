<script setup lang="ts">
import { computed } from 'vue'
import { NAlert, NCheckbox, NFormItem, NInput, NSelect } from 'naive-ui'
import { installAccessError, type InstallAccessForm } from '../utils/installAccess'

const props = defineProps<{ modelValue: InstallAccessForm; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [InstallAccessForm] }>()
function update<K extends keyof InstallAccessForm>(key: K, value: InstallAccessForm[K]) {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}
const error = computed(() => installAccessError(props.modelValue))
const passwordFeedback = computed(() => props.modelValue.confirmPassword && props.modelValue.rootPassword !== props.modelValue.confirmPassword ? '两次密码不一致' : undefined)
</script>

<template>
  <section class="install-access" aria-label="系统访问设置">
    <div class="access-heading"><strong>系统访问设置</strong><span>仅用于本次安装</span></div>
    <NFormItem label="主机名（可选）" :show-feedback="false">
      <NInput :value="modelValue.hostname" @update:value="update('hostname', $event)" :disabled="disabled" :maxlength="63" autocomplete="off" :input-props="{ 'aria-label': '安装主机名' }" placeholder="留空使用装机方案中的主机名" />
    </NFormItem>
    <div class="access-passwords">
      <NFormItem label="root 密码" :show-feedback="false">
        <NInput :value="modelValue.rootPassword" @update:value="update('rootPassword', $event)" type="password" show-password-on="click" autocomplete="new-password" :disabled="disabled" :input-props="{ 'aria-label': '安装 root 密码' }" placeholder="至少 8 个字符；仅公钥时可留空" />
      </NFormItem>
      <NFormItem label="确认 root 密码" :validation-status="passwordFeedback ? 'error' : undefined" :feedback="passwordFeedback" :show-feedback="!!passwordFeedback">
        <NInput :value="modelValue.confirmPassword" @update:value="update('confirmPassword', $event)" type="password" show-password-on="click" autocomplete="new-password" :disabled="disabled" :input-props="{ 'aria-label': '确认安装 root 密码' }" placeholder="再次输入密码" />
      </NFormItem>
    </div>
    <p class="access-help">密码用于 root 控制台登录；留空则锁定 root 密码。SSH 密码登录需在下方单独开启。</p>
    <NCheckbox :checked="modelValue.addSSHKeys" @update:checked="update('addSSHKeys', $event)" :disabled="disabled">添加 SSH 公钥</NCheckbox>
    <NFormItem v-if="modelValue.addSSHKeys" label="SSH 公钥" :show-feedback="false" class="access-keys">
      <NInput :value="modelValue.sshKeys" @update:value="update('sshKeys', $event)" type="textarea" :autosize="{ minRows: 3, maxRows: 6 }" :disabled="disabled" :input-props="{ 'aria-label': '安装 SSH 公钥' }" placeholder="每行一条 ssh-ed25519、ssh-rsa 或 ECDSA 公钥，最多 20 条。请勿粘贴私钥。" />
    </NFormItem>
    <p v-if="modelValue.addSSHKeys" class="access-help">公钥写入 admin 和 root 账户；admin 可使用 sudo。root 是否允许远程登录由下方策略决定。</p>
    <NFormItem label="root SSH 登录" :show-feedback="false" class="access-policy">
      <NSelect :value="modelValue.rootSSHLogin" @update:value="update('rootSSHLogin', $event)" :disabled="disabled" aria-label="root SSH 登录策略" :options="[
        { label: '禁止 root SSH 登录（默认）', value: 'no' },
        { label: '仅允许 root 公钥登录', value: 'prohibit-password' },
        { label: '允许 root 密码及公钥登录', value: 'yes' }
      ]" />
    </NFormItem>
    <NAlert v-if="error" type="info" :show-icon="false" :bordered="false" class="access-hint">{{ error }}</NAlert>
    <p v-else class="access-help">{{ modelValue.rootSSHLogin === 'yes' ? 'SSH 密码认证已开启，使用 root 和本次设置的密码登录。' : modelValue.rootSSHLogin === 'prohibit-password' ? 'SSH 密码认证关闭，可使用公钥登录 admin 或 root。' : modelValue.addSSHKeys ? 'SSH 密码认证关闭，可使用公钥登录 admin，再执行 sudo。' : 'SSH 密码认证关闭；安装后使用 root 密码通过控制台登录。' }}</p>
  </section>
</template>

<style scoped>
.install-access { margin: 18px 0; padding: 18px 0; border-top: 1px solid var(--color-border); border-bottom: 1px solid var(--color-border); }
.access-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 16px; }
.access-heading span, .access-help { color: var(--color-text-secondary); font-size: 12px; }
.access-passwords { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; margin-top: 14px; }
.access-help { line-height: 1.7; margin: 8px 0 14px; overflow-wrap: anywhere; }
.access-keys, .access-policy { margin-top: 14px; }
.access-hint { margin-top: 12px; }
@media (max-width: 540px) { .access-passwords { grid-template-columns: 1fr; } }
</style>
