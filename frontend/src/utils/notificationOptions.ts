export const channelTypes = [
  {label:'飞书 / Lark',value:'lark'}, {label:'企业微信',value:'wechat'}, {label:'钉钉',value:'dingtalk'}, {label:'通用 Webhook',value:'webhook'}, {label:'邮件',value:'email'}
]
export const metricOptions = [{label:'BMC 连续采集失败',value:'bmc.collection_failed'}, {label:'设备健康状态',value:'bmc.health'}]
export const severityOptions = [{label:'提示及以上',value:'info'}, {label:'警告及以上',value:'warning'}, {label:'严重及以上',value:'critical'}, {label:'紧急',value:'emergency'}]
export const operatorOptions = [{label:'大于',value:'gt'}, {label:'大于等于',value:'gte'}, {label:'小于',value:'lt'}, {label:'小于等于',value:'lte'}, {label:'等于',value:'eq'}, {label:'不等于',value:'ne'}]
export const healthOptions = [{label:'正常（0）',value:0}, {label:'警告（1）',value:1}, {label:'严重（2）',value:2}]
export const channelName = (kind: unknown) => channelTypes.find(row => row.value === kind)?.label || '未知渠道'
export const metricName = (metric: unknown) => metricOptions.find(row => row.value === metric)?.label || '旧版指标（请重新选择）'
export const severityName = (severity: unknown) => severityOptions.find(row => row.value === severity)?.label || '未知等级'
export function conditionName(row: {operator?:unknown; threshold?:unknown}) {
  if (row.operator === 'collection_failed') return '连续失败 2 次时'
  return `${operatorOptions.find(option => option.value === row.operator)?.label || '未知条件'} ${healthOptions.find(option => option.value === Number(row.threshold))?.label || row.threshold}`
}
export type ChannelDraft = {name:string; kind:string; enabled:boolean; secret:string; auth:boolean; host:string; port:number | null; username:string; from:string; to:string; tls:boolean}
export function channelDraft(row?: Record<string, any>): ChannelDraft {
  const config = row?.config || {}
  return {name:row?.name || '', kind:row?.kind || 'wechat', enabled:row?.enabled ?? true, secret:'', auth:!!config.username, host:config.host || '', port:config.port || 587, username:config.username || '', from:config.from || '', to:config.to || '', tls:config.tls ?? true}
}
export function channelPayload(form: ChannelDraft, savedSecret: boolean) {
  if (!form.name.trim() || form.name.trim().length > 255) throw Error('请填写渠道名称，最多 255 个字符')
  if (!channelTypes.some(row => row.value === form.kind)) throw Error('请选择支持的渠道类型')
  if (form.kind === 'email') {
    if (!form.host.trim() || /[\s/]/.test(form.host.trim())) throw Error('请填写 SMTP 主机名或 IP，不含协议、路径或空格')
    if (!Number.isInteger(form.port) || form.port! < 1 || form.port! > 65535) throw Error('SMTP 端口需为 1 到 65535 的整数')
    for (const [name, value] of [['发件地址', form.from], ['收件地址', form.to]]) if (!/^[^\s@<>;,]+@[^\s@<>;,]+$/.test(value!.trim())) throw Error(`请填写有效的单个${name}`)
    if (form.auth && (!form.username.trim() || (!form.secret && !savedSecret))) throw Error('启用 SMTP 认证时，请填写账号及密码')
    if (form.auth && !form.tls && form.port !== 465) throw Error('SMTP 认证需要开启 STARTTLS，或使用 465 端口')
    return {name:form.name.trim(), kind:form.kind, enabled:form.enabled, config:{host:form.host.trim(), port:form.port, username:form.auth ? form.username.trim() : '', from:form.from.trim(), to:form.to.trim(), tls:form.tls}, ...(!form.auth ? {secret:''} : form.secret ? {secret:form.secret} : {})}
  }
  const secret = form.secret.trim()
  if (!secret && !savedSecret) throw Error('请填写机器人的完整 Webhook 地址')
  if (secret) {
    let url: URL
    try { url = new URL(secret) } catch { throw Error('Webhook 地址格式无效') }
    if (url.protocol !== 'https:' || url.username || url.password || url.hash || (url.port && url.port !== '443')) throw Error('Webhook 需使用 HTTPS，不包含用户名、密码、片段或自定义端口')
    const domains: Record<string,string[]> = {lark:['open.feishu.cn','open.larksuite.com'], wechat:['qyapi.weixin.qq.com'], dingtalk:['oapi.dingtalk.com']}
    if (domains[form.kind] && !domains[form.kind]!.includes(url.hostname)) throw Error(`请填写${channelName(form.kind)}机器人的正确 Webhook 地址`)
  }
  return {name:form.name.trim(), kind:form.kind, enabled:form.enabled, config:{}, ...(secret ? {secret} : {})}
}
