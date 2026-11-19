export type RootSSHLogin = 'no' | 'prohibit-password' | 'yes'
export type InstallAccessForm = {
  hostname: string
  rootPassword: string
  confirmPassword: string
  addSSHKeys: boolean
  sshKeys: string
  rootSSHLogin: RootSSHLogin
}

export function newInstallAccess(): InstallAccessForm {
  return { hostname: '', rootPassword: '', confirmPassword: '', addSSHKeys: false, sshKeys: '', rootSSHLogin: 'yes' }
}

export function installAccessPayload(form: InstallAccessForm) {
  return {
    hostname: form.hostname.trim(),
    root_password: form.rootPassword,
    ssh_authorized_keys: form.addSSHKeys ? form.sshKeys.split(/\r?\n/).map(line => line.trim()).filter(Boolean) : [],
    root_ssh_login: form.rootSSHLogin
  }
}

// The API performs full cryptographic public-key parsing as well.
export function installAccessError(form: InstallAccessForm): string {
  const data = installAccessPayload(form)
  if (data.hostname && !/^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/.test(data.hostname)) return '主机名须为 1–63 位字母、数字或中划线，且不能以中划线开头或结尾'
  if (data.root_password && (Array.from(data.root_password).length < 8 || new TextEncoder().encode(data.root_password).length > 128 || /[\0\r\n]/.test(data.root_password) || !data.root_password.trim())) return 'root 密码至少 8 个字符、最多 128 字节，不能含换行或空字符'
  if (form.rootPassword !== form.confirmPassword) return '两次输入的 root 密码不一致'
  if (form.addSSHKeys && !data.ssh_authorized_keys.length) return '请添加至少一条 SSH 公钥，或关闭添加公钥'
  if (data.ssh_authorized_keys.length > 20) return '最多添加 20 条 SSH 公钥'
  if (data.ssh_authorized_keys.some(key => key.length > 16384 || !/^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(?:256|384|521)) [A-Za-z0-9+/]+={0,3}(?:\s.*)?$/.test(key))) return 'SSH 公钥格式无效，每行粘贴一条完整 OpenSSH 公钥，请勿粘贴私钥'
  if (data.root_ssh_login === 'prohibit-password' && !data.ssh_authorized_keys.length) return 'root 仅公钥登录需要至少一条 SSH 公钥'
  return ''
}
